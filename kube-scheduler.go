package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"time"

	honeybadger "github.com/honeybadger-io/honeybadger-go"
	"github.com/joho/godotenv"
	"github.com/robfig/cron"
	kube "github.com/wearemolecule/kubeclient"
	"golang.org/x/build/kubernetes/api"
	"golang.org/x/net/context"
	"gopkg.in/yaml.v2"
)

var configDir string
var kubeClient *kube.Client
var jobLock map[string]string

func init() {
	flag.StringVar(&configDir, "dir", ".", "Directory where config is located")
}

func main() {
	configureHoneybadger()
	defer honeybadger.Monitor()

	jobLock = make(map[string]string)
	flag.Parse()
	err := godotenv.Load()
	if err != nil {
		log.Println(err)
	}

	kubeClient, err = kube.GetKubeClientFromEnv()
	if err != nil {
		nErr := fmt.Errorf("Failed to connect to kubernetes. Error: %v", err)
		honeybadger.Notify(nErr, honeybadger.Fingerprint{fmt.Sprintf("%d", time.Now().Unix())})
		panic(nErr)
	}

	b, err := ioutil.ReadFile(filePath("schedule.yml"))
	if err != nil {
		nErr := fmt.Errorf("Unable to read schedule yaml, error: %v", err)
		honeybadger.Notify(nErr, honeybadger.Fingerprint{fmt.Sprintf("%d", time.Now().Unix())})
		log.Fatal(nErr)
	}

	var config JobList
	err = yaml.Unmarshal(b, &config)
	if err != nil {
		nErr := fmt.Errorf("Unable to unmarshal schedule yaml, error: %v", err)
		honeybadger.Notify(nErr, honeybadger.Fingerprint{fmt.Sprintf("%d", time.Now().Unix())})
		log.Fatal(nErr)
	}

	log.Println(config)
	c := cron.New()
	for _, job := range config {
		c.AddJob(job.Cron, job)
	}

	c.Start()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, os.Kill)

	s := <-sigs
	log.Println("Got signal: ", s)
}

type JobList map[string]Job

type Job struct {
	Cron        string
	Template    string
	Description string
	Args        []string
	Namespace   string
}

func (j Job) Run() {
	if _, ok := jobLock[j.Template]; ok {
		nErr := fmt.Errorf("Unable to start new job (%s) because it is already running", j.Description)
		honeybadger.Notify(nErr, honeybadger.Fingerprint{fmt.Sprintf("%d", time.Now().Unix())})
		log.Println(nErr)
		return
	}
	log.Println("Running", j.Description)

	//TODO not thread safe
	jobLock[j.Template] = "started"
	defer delete(jobLock, j.Template)

	if err := createTaskPod(j); err != nil {
		nErr := fmt.Errorf("Failed to create task pod for job %s, error: %v", j.Description, err)
		honeybadger.Notify(nErr, honeybadger.Fingerprint{fmt.Sprintf("%d", time.Now().Unix())})
		log.Println(nErr)
		return
	}

	log.Println("Done", j.Description)
}

func filePath(filename string) string {
	return fmt.Sprintf("%s/%s", configDir, filename)
}

func createTaskPod(j Job) error {
	ctx := context.TODO()

	podData, err := ioutil.ReadFile(filePath(j.Template))
	pod := api.Pod{}
	if err != nil {
		return errors.New(fmt.Sprintf("Error reading task pod.\n%v", err))
	}

	err = json.Unmarshal(podData, &pod)
	if err != nil {
		return errors.New(fmt.Sprintf("Error parsing task pod.\n%v", err))
	}

	pod.Spec.Containers[0].Args = j.Args
	pod.ObjectMeta.Namespace = j.Namespace

	newPod, err := kubeClient.CreatePod(ctx, &pod)
	if err != nil {
		return errors.New(fmt.Sprintf("Error creating task pod.\n%v", err))
	}

	statuses, err := kubeClient.WatchPod(ctx, newPod.Namespace, newPod.Name, newPod.ResourceVersion)
	if err != nil {
		return errors.New(fmt.Sprintf("Error watching task pod.\n%v", err))
	}

	for status := range statuses {
		podStatus := status.Pod.Status
		if podStatus.Phase == "Failed" {
			_ = kubeClient.DeletePod(ctx, newPod.Namespace, newPod.Name)
			return errors.New(fmt.Sprintf("Task pod %s in namespace %s failed.", newPod.Name, newPod.Namespace))
		}
		if podStatus.Phase == "Succeeded" {
			if logs, err := kubeClient.PodLog(ctx, newPod.Namespace, newPod.Name); err != nil {
				log.Println("Failed to get logs for pod %s in namespace %s\n", newPod.Name, newPod.Namespace)
			} else {
				log.Println(logs)
			}
			if err = kubeClient.DeletePod(ctx, newPod.Namespace, newPod.Name); err != nil {
				nErr := fmt.Errorf("Failed to delete task pod for job %s, error: %v", j.Description, err)
				honeybadger.Notify(nErr, honeybadger.Fingerprint{fmt.Sprintf("%d", time.Now().Unix())})
				log.Println(nErr)
			}
			break
		}
	}

	return nil
}

func configureHoneybadger() {
	honeybadger.Configure(honeybadger.Configuration{APIKey: os.Getenv("HONEYBADGER_API_KEY")})
}
