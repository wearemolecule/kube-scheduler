package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	"github.com/wearemolecule/kube-scheduler/kubernetes"
	"github.com/wearemolecule/kube-scheduler/notifier"
	"github.com/wearemolecule/kube-scheduler/scheduler"
)

var (
	kubernetesConfigPath string
	scheduleConfigPath   string
	scheduleConfigName   string
)

func init() {
	flag.StringVar(&scheduleConfigName, "schedule-name", "schedule.yml", "name of schedule config file (defaults to schedule.yml)")
	flag.StringVar(&scheduleConfigPath, "schedule-path", ".", "absolute path to schedule yaml (defaults to current dir)")
	flag.StringVar(&kubernetesConfigPath, "kube-config-path", "", "absolute path to kubernetes credentials dir")
}

func main() {
	flag.Parse()
	birthCry()

	namespace := os.Getenv("SCHEDULER_NAMESPACE")

	notifier := notifier.NewClient(namespace)

	kubernetesClient, err := kubernetes.NewClient(kubernetesConfigPath, scheduleConfigPath)
	if err != nil {
		notifier.Notify("Failed to create kubernetes client", err)
		return
	}

	scheduler, err := scheduler.NewClient(scheduleConfigPath, scheduleConfigName)
	if err != nil {
		notifier.Notify("Failed to create scheduler", err)
		return
	}

	done := make(chan int)

	go schedule(scheduler, kubernetesClient, notifier, done)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill)
	s := <-sigs
	done <- 1
	glog.Infof("Got signal: %s", s)
}

func schedule(
	scheduler scheduler.ClientInterface,
	kubeClient kubernetes.ClientInterface,
	notifier notifier.ClientInterface,
	done chan int,
) {
	for name, job := range scheduler.JobList() {
		if job.Cron == "" {
			continue
		}

		// Must copy the variables to retain scope inside the AsyncAddScheduledJob func
		nameCopy := name
		jobCopy := job

		glog.Infof("Found job %s with schedule %s", nameCopy, jobCopy.Cron)
		scheduler.AsyncAddScheduledJob(job, func() {
			glog.Info("Running job ", nameCopy)
			defer glog.Info("Finished job ", nameCopy)

			if err := run(nameCopy, jobCopy, kubeClient, scheduler); err != nil {
				notifier.Notify(fmt.Sprintf("Unable to create/run job %s", nameCopy), err)
			}
		})
	}

	scheduler.Start()
	defer scheduler.Stop()

	for {
		select {
		case <-done:
			return
		}
	}
}

func run(name string, job scheduler.Job, kubeClient kubernetes.ClientInterface, scheduler scheduler.ClientInterface) error {
	if scheduler.Running(name, job) {
		glog.Warningf("Unable to start %s becuase it is already running", name)
		return nil
	}

	scheduler.StartJob(name, job)
	defer scheduler.FinishJob(name, job)

	if err := kubeClient.RunJob(name, job); err != nil {
		return errors.Wrap(err, fmt.Sprintf("Unable to create job %s", name))
	}

	return nil
}

var (
	buildstamp = "none provided"
	githash    = "none provided"
)

func birthCry() {
	log.Println("Git Commit Hash:", githash)
	log.Println("Build Time:", buildstamp)
}
