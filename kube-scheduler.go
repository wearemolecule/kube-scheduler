package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/golang/glog"
	honeybadger "github.com/honeybadger-io/honeybadger-go"
	"github.com/joho/godotenv"
	"github.com/pkg/errors"
	"github.com/robfig/cron"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api"
	"k8s.io/client-go/1.4/pkg/apis/batch/v1"
	"k8s.io/client-go/1.4/pkg/fields"
	"k8s.io/client-go/1.4/rest"
	"k8s.io/client-go/1.4/tools/clientcmd"
)

var (
	kubeClient   *kubernetes.Clientset
	manager      Manager
	configPath   string
	configDir    string
	scheduleName string
)

func init() {
	flag.StringVar(&configDir, "config", ".", "path to schedule yaml")
	flag.StringVar(&scheduleName, "schedule-name", "schedule.yml", "name of schedule config file")
	flag.StringVar(&configPath, "kubeconfig", "", "absolute path to kubernetes credentials dir")

	if configPath == "" {
		configPath = os.Getenv("KUBE_CONFIG_PATH")
	}

	if os.Getenv("SCHEDULE_NAME") != "" {
		scheduleName = os.Getenv("SCHEDULE_NAME")
	}

	configureHoneybadger()
}

func main() {
	flag.Parse()
	godotenv.Load()
	glog.Info("Kube Scheduler")

	var err error
	kubeClient, err = createKubernetesClient()
	if err != nil {
		honeybadger.Notify(
			"Scheduler could not create kubernetes client",
			honeybadger.Context{"error": err}, honeybadger.Fingerprint{time.Now().String()},
		)
		glog.Fatalf("Could not create kubernetes client: %s", err)
	}

	manager, err = createScheduleManager(filePath(scheduleName))
	if err != nil {
		honeybadger.Notify(
			"Scheduler could not create job manager",
			honeybadger.Context{"error": err}, honeybadger.Fingerprint{time.Now().String()},
		)
		glog.Fatalf("Could not create manager: %s", err)
	}

	cronManager := cron.New()
	// loop over the jobs and add to the cronManager
	for name, job := range manager.jobList {
		if job.Cron == "" {
			glog.Infof("Ignoring job %s (%s) without schedule", name, job.Description)
			continue // Skip adding empty schedules to the cronManager
		}
		glog.Infof("Adding job %s (%s) with schedule %s", name, job.Description, job.Cron)
		go func(name string, job Job) {
			cronManager.AddFunc(job.Cron, func() {
				glog.Infof("Running %s...", name)
				if err := job.Run(); err != nil {
					glog.Warningf("Unable to create & run job: %s", err)
					honeybadger.Notify(fmt.Sprintf("Unable to schedule %s", name), honeybadger.Context{"error": err})
				}
				glog.V(1).Infof("Finished job %s", name)
			})
		}(name, job)
	}
	cronManager.Start()
	defer cronManager.Stop()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill)
	s := <-sigs
	glog.Infof("Got signal: %s", s)
}

func createKubernetesClient() (*kubernetes.Clientset, error) {
	var (
		kubeConfig *rest.Config
		err        error
	)

	// If no config path is given assume we are in the cluster
	if configPath == "" {
		kubeConfig, err = rest.InClusterConfig()
	} else {
		kubeConfig, err = clientcmd.BuildConfigFromFlags("", configPath)
	}

	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to kubernetes")
	}

	return kubernetes.NewForConfig(kubeConfig)
}

func createScheduleManager(schedulePath string) (Manager, error) {
	scheduleData, err := ioutil.ReadFile(schedulePath)
	if err != nil {
		return Manager{}, errors.Wrap(err, "Unable to read schedule yaml")
	}

	var config JobList
	if err := yaml.Unmarshal(scheduleData, &config); err != nil {
		return Manager{}, errors.Wrap(err, "Unable to unmarshal schedule yaml")
	}

	return Manager{
		jobList: config,
		jobLock: make(map[string]string),
		mutex:   &sync.Mutex{},
	}, nil
}

type JobList map[string]Job

type Manager struct {
	jobList JobList
	jobLock map[string]string
	mutex   *sync.Mutex
}

func (m *Manager) ReadFromJobLock(template string) (string, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	v, ok := m.jobLock[template]
	return v, ok
}

func (m *Manager) WriteToJobLock(template, state string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.jobLock[template] = state
}

func (m *Manager) DeleteFromJobLock(template string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.jobLock, template)
}

func (m *Manager) NameFromJob(j Job) (string, error) {
	for k, v := range m.jobList {
		if v.Equal(j) {
			return k, nil
		}
	}

	return "", fmt.Errorf("Job is not in job list")
}

type Job struct {
	Cron        string
	Template    string
	Description string
	Args        []string
	Namespace   string
}

func (j Job) Equal(job Job) bool {
	return j.Cron == job.Cron &&
		j.Template == job.Template &&
		j.Description == job.Description &&
		j.Namespace == job.Namespace
}

func (j Job) Run() error {
	jobName, err := manager.NameFromJob(j)
	if err != nil {
		return errors.Wrap(err, "Unable to get name from job")
	}

	if _, ok := manager.ReadFromJobLock(jobName); ok {
		glog.Warningf("Unable to start %s becuase it is already running", jobName)
		return nil
	}

	manager.WriteToJobLock(jobName, "started")
	defer manager.DeleteFromJobLock(jobName)

	if err := createTaskJob(jobName, j); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Failed to created job %s", jobName))
	}

	return nil
}

func createTaskJob(jobName string, j Job) error {
	jobData, err := ioutil.ReadFile(filePath(j.Template))
	if err != nil {
		return errors.Wrap(err, "Error reading job template")
	}

	job := v1.Job{}
	if err = json.Unmarshal(jobData, &job); err != nil {
		return errors.Wrap(err, "Error parsing task pod")
	}

	glog.V(2).Infof("For %s found args: %v", jobName, j.Args)
	glog.V(2).Infof("For %s found namespace: %s", jobName, j.Namespace)

	job.Spec.Template.Spec.Containers[0].Args = j.Args
	job.ObjectMeta.Namespace = j.Namespace

	jobsClient := kubeClient.Batch().Jobs(j.Namespace)
	newJob, err := jobsClient.Create(&job)
	if err != nil {
		return errors.Wrap(err, "Error creating task job")
	}

	glog.V(2).Infof("Created kubernetes job %s", newJob.Name)

	events, err := jobsClient.Watch(api.ListOptions{
		FieldSelector:   fields.OneTermEqualSelector("metadata.name", newJob.Name),
		Watch:           true,
		ResourceVersion: newJob.ResourceVersion,
	})
	if err != nil {
		return errors.Wrap(err, "Error creating job watcher")
	}
	defer events.Stop()

	glog.V(2).Infof("Watching kubernetes job %s for status events", newJob.Name)

	var jobErr error
	for event := range events.ResultChan() {
		job := event.Object.(*v1.Job)
		if len(job.Status.Conditions) > 0 {
			if job.Status.Conditions[0].Type == v1.JobComplete {
				break
			}

			if job.Status.Conditions[0].Type == v1.JobFailed {
				jobErr = fmt.Errorf("Error creating job task")
				break
			}
		}
	}

	if err = jobsClient.Delete(newJob.Name, &api.DeleteOptions{}); err != nil {
		return errors.Wrap(err, "Error deleting job.")
	}

	glog.V(2).Infof("Deleted kubernetes job %s", newJob.Name)

	return jobErr
}

func filePath(filename string) string {
	return fmt.Sprintf("%s/%s", configDir, filename)
}

func configureHoneybadger() {
	honeybadger.Configure(honeybadger.Configuration{APIKey: os.Getenv("HONEYBADGER_API_KEY"), Env: os.Getenv("NAMESPACE")})
}
