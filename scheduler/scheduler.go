// The scheduler package provides a client to run jobs on a cron schedule and managed running jobs.
package scheduler

import (
	"fmt"
	"io/ioutil"
	"sync"

	"github.com/pkg/errors"
	"github.com/robfig/cron"
	"gopkg.in/yaml.v2"
)

type ClientInterface interface {
	AsyncAddScheduledJob(Job, func())
	JobList() jobList
	Start()
	Stop()
	Running(string, Job) bool
	StartJob(string, Job)
	FinishJob(string, Job)
}

func NewClient(scheduleConfigPath, scheduleConfigName string) (*client, error) {
	data, err := ioutil.ReadFile(fullPath(scheduleConfigPath, scheduleConfigName))
	if err != nil {
		return nil, errors.Wrap(err, "Unable to open schedule config file")
	}

	var jobList jobList
	if err := yaml.Unmarshal(data, &jobList); err != nil {
		return nil, errors.Wrap(err, "Unable to unmarshal schedule yaml")
	}

	return &client{jobList: jobList, jobLock: make(map[string]string), mutex: &sync.Mutex{}, cronRunner: cron.New()}, nil
}

// AsyncAddScheduledJob will schedule the run of the given function using the jobs cron field.
// Must call client.Start() before jobs are run.
func (c *client) AsyncAddScheduledJob(job Job, fn func()) {
	c.cronRunner.AddFunc(job.Cron, fn)
}

// Start will begin the process of running the scheduled jobs.
// Must call client.Stop() once the process is finished.
func (c *client) Start() {
	c.cronRunner.Start()
}

func (c *client) Stop() {
	c.cronRunner.Stop()
}

// Running will determine if the given job is already running, scoped to namespace, and is thread-safe.
// Must call client.StartJob() to add it to the running queue.
// Must call client.FinishJob() to remove it from the running queue.
func (c *client) Running(name string, job Job) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	_, ok := c.jobLock[jobKey(name, job)]
	return ok
}

// StartJob will add the job to the running queue and is thread-safe.
func (c *client) StartJob(name string, job Job) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.jobLock[jobKey(name, job)] = "running"
}

// FinishJob will remove the job from the running queue and is thread-safe.
func (c *client) FinishJob(name string, job Job) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.jobLock, jobKey(name, job))
}

func (c *client) JobList() jobList {
	return c.jobList
}

type Job struct {
	Cron        string `yaml:"cron"`
	Template    string `yaml:"template"`
	Description string `yaml:"description"`
	//Allows overriding the image given in the job spec
	Image     string   `yaml:"image"`
	Args      []string `yaml:"args"`
	Namespace string   `yaml:"namespace"`
}

type client struct {
	jobList    jobList
	jobLock    map[string]string
	mutex      *sync.Mutex
	cronRunner *cron.Cron
}

type jobList map[string]Job

func fullPath(dir, filename string) string {
	return fmt.Sprintf("%s/%s", dir, filename)
}

// We can safely assume the job name will be unique.
// Jobs need to be able to be run in multiple namespaces simultaneously.
func jobKey(name string, job Job) string {
	return name + job.Namespace
}
