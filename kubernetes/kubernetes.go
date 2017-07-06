// The kubernetes package is a wrapper around the k8s.io/client-go library.
package kubernetes

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	"github.com/wearemolecule/kube-scheduler/scheduler"
	"k8s.io/client-go/1.4/kubernetes"
	"k8s.io/client-go/1.4/pkg/api"
	"k8s.io/client-go/1.4/pkg/apis/batch/v1"
	"k8s.io/client-go/1.4/pkg/fields"
	"k8s.io/client-go/1.4/pkg/watch"
	"k8s.io/client-go/1.4/rest"
	"k8s.io/client-go/1.4/tools/clientcmd"
)

type ClientInterface interface {
	RunJob(string, scheduler.Job) error
}

func NewClient(kubeConfigPath, schedulerConfigPath string) (*client, error) {
	var (
		kubeConfig *rest.Config
		err        error
	)

	// If no config path is given assume we are in the cluster
	if kubeConfigPath == "" {
		kubeConfig, err = rest.InClusterConfig()
	} else {
		kubeConfig, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath)
	}

	if err != nil {
		return nil, errors.Wrap(err, "Failed to connect to kubernetes")
	}

	kubeClient, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, errors.Wrap(err, "Unable to create kubernetes client from config")
	}

	return &client{kubeClient, schedulerConfigPath}, nil
}

type client struct {
	client              *kubernetes.Clientset
	schedulerConfigPath string
}

// RunJob will create a k8s/batch.v1.Job inside the kubernetes cluster given a job template file.
//
// We have seen three operations fail due to timeout issues: create, delete, watch.
// All of these operations will be retried 3 timtes automatically.
func (c *client) RunJob(name string, job scheduler.Job) error {
	data, err := ioutil.ReadFile(c.jobPath(job))
	if err != nil {
		return errors.Wrap(err, "Error reading job template")
	}

	kubeJob := v1.Job{}
	if err = json.Unmarshal(data, &kubeJob); err != nil {
		return errors.Wrap(err, "Error parsing task pod")
	}

	glog.V(2).Infof("For %s found args: %v", name, job.Args)
	glog.V(2).Infof("For %s found namespace: %s", name, job.Namespace)
	firstContainer := &kubeJob.Spec.Template.Spec.Containers[0]
	firstContainer.Args = job.Args
	if job.Image != "" {
		firstContainer.Image = job.Image
	}
	kubeJob.ObjectMeta.Namespace = job.Namespace

	clusterJob, err := c.createJob(kubeJob, job.Namespace)
	if err != nil {
		return errors.Wrap(err, "Error creating kubernetes job")
	}
	defer c.deleteJob(clusterJob, job.Namespace)

	events, err := c.watchJob(clusterJob, job.Namespace)
	if err != nil {
		return errors.Wrap(err, "Error creating job watcher")
	}
	defer events.Stop()

	for event := range events.ResultChan() {
		job := event.Object.(*v1.Job)
		if len(job.Status.Conditions) > 0 {
			condition := job.Status.Conditions[0]
			if condition.Type == v1.JobComplete {
				return nil
			}

			if condition.Type == v1.JobFailed {
				return fmt.Errorf(condition.Message)
			}
		}
	}

	return nil
}

func (c *client) createJob(job v1.Job, namespace string) (*v1.Job, error) {
	glog.V(2).Infof("Created kubernetes job %s", job.Name)
	thing, err := autoRetry(func() (interface{}, error) {
		jobsClient := c.client.Batch().Jobs(namespace)
		return jobsClient.Create(&job)
	})

	// Check if the conversion went ok (nil values would otherwise cause panic)
	if j, ok := thing.(*v1.Job); ok {
		return j, err
	}

	return nil, err
}

func (c *client) deleteJob(job *v1.Job, namespace string) error {
	glog.V(2).Infof("Deleted kubernetes job %s", job.Name)
	_, err := autoRetry(func() (interface{}, error) {
		jobsClient := c.client.Batch().Jobs(namespace)
		err := jobsClient.Delete(job.Name, &api.DeleteOptions{})
		return nil, err
	})

	return err
}

func (c *client) watchJob(job *v1.Job, namespace string) (watch.Interface, error) {
	glog.V(2).Infof("Watching kubernetes job %s for status events", job.Name)
	thing, err := autoRetry(func() (interface{}, error) {
		jobsClient := c.client.Batch().Jobs(namespace)
		return jobsClient.Watch(api.ListOptions{
			FieldSelector:   fields.OneTermEqualSelector("metadata.name", job.Name),
			Watch:           true,
			ResourceVersion: job.ResourceVersion,
		})
	})

	return thing.(watch.Interface), err
}

func autoRetry(fn func() (interface{}, error)) (interface{}, error) {
	var attempts int
	var err error
	var thing interface{}

	for attempts < 3 {
		thing, err = fn()
		if err == nil {
			return thing, nil
		}

		attempts += 1
		time.Sleep(1 * time.Second)
	}

	return nil, err

}

func (c *client) jobPath(job scheduler.Job) string {
	return fmt.Sprintf("%s/%s", c.schedulerConfigPath, job.Template)
}
