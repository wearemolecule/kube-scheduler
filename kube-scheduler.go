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
	jobLock = make(map[string]string)
	flag.Parse()
	err := godotenv.Load()
	if err != nil {
		log.Println(err)
	}

	kubeClient, err = kube.GetKubeClientFromEnv()
	if err != nil {
		panic(fmt.Errorf("Failed to connect to kubernetes. Error: %v", err))
	}

	b, err := ioutil.ReadFile(filePath("schedule.yml"))
	if err != nil {
		log.Fatal(err)
	}

	var config JobList
	err = yaml.Unmarshal(b, &config)
	if err != nil {
		log.Fatal(err)
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
		log.Printf("%s is already running\n", j.Description)
		return
	}
	log.Println("Running", j.Description)
	//TODO not thread safe
	jobLock[j.Template] = "started"
	err := createTaskPod(j)
	delete(jobLock, j.Template)
	if err != nil {
		log.Println("Failed", err)
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
			return errors.New(fmt.Sprintf("Task pod failed.\n%v", err))
		}
		if podStatus.Phase == "Succeeded" {
			logs, err := kubeClient.PodLog(ctx, newPod.Namespace, newPod.Name)
			if err != nil {
				log.Println("Failed to get logs")
			}
			log.Println(logs)
			_ = kubeClient.DeletePod(ctx, newPod.Namespace, newPod.Name)
			break
		}
	}

	return nil
}
