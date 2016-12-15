![Molecule Software](https://avatars1.githubusercontent.com/u/2736908?v=3&s=100 "Molecule Software")
# Kube Scheduler

A kubernetes utility to run docker images on a cron-like schedule.

## Why We Made This

We love (and use!) [kubernetes](http://kubernetes.io/), [docker](https://www.docker.com/) and [microservices](http://martinfowler.com/articles/microservices.html?ref=scaleyourcode.com) so instead of trying to shove new schedulable jobs into our existing [Resque](https://github.com/resque/resque) architecture we built this utility to schedule and run docker images inside of our kubernetes cluster.

## To Use

The scheduler requires a kubernetes cluster. If you have one set up, great keep reading! If not there are [guides](http://kubernetes.io/docs/getting-started-guides/) available to walk you through setting up a local or hosted solution ([minikube](http://kubernetes.io/docs/getting-started-guides/minikube/) is a really simple way to get started).

Once your cluster is configured you need a job/task to run. Our workflow looks like this:
1) Write the code for our job/task.
2) Build & push a docker image to our hosted container registry (we use [quay](https://quay.io/)).
3) Define a `my-task-pod.json` configuration file.
4) Add the job/task to the scheduler's configuration file.
5) Re-deploy the scheduler to pick up the new configuration

## Getting things Running

1. Clone this repo.
1. Run `make setup` (which uses [glide](https://github.com/Masterminds/glide)).
1. Set environment variables.
    * `CERTS_PATH` should be location of your kubernetes credentials.
    * `KUBERNETES_SERVICE_HOST` should be the host name of your kubernetes cluster.
    * `KUBERNETES_SERVICE_PORT` should be the port of your kubernetes cluster.
    * `HONEYBADGER_API_KEY` should be the key to your honeybadger project (*OPTIONAL*).
1. Create a pod definition for your job/task (a sample is [here](https://github.com/wearemolecule/kube-scheduler/blob/master/nymex_prelims.json)).
1. Create `schedule.yml` configuration file (a sample is [here](https://github.com/wearemolecule/kube-scheduler/blob/master/schedule.yml.sample)).
1. Start the service.
   * `go run kube-scheduler.go`
   * The scheduler takes an optional flag to point to the directory of the `schedule.yml` configuration file (defaults to current directory).

## Gotchas

* The schedule for a task is defined using [Go's cron](https://godoc.org/github.com/robfig/cron) syntax.
* The timezone is __local to the scheduler__ with no way to specify an alternative timezone.
* Active jobs are __always unique__. The scheduler will prevent a job from starting if the same job is already running.
* An invalid cron syntax will cause a panic in the scheduler from a downstream library
* Duplicate job definitions names will only run the last definition, this is a quirk of yaml parsing
* You can leave a cron as empty string `""` to disable an existing job. This does not stop any active configutations/jobs
* The logs produced by the scheduler are any of its own logs and all logs from tasks/jobs.
* We log (and create optional honeybadger messages) when a pod fails to start or finish in a "successful" state or when a job is scheduled to start while another instance of the same job is still active.

## Copyright and License

Copyright © 2016 Molecule Software, Inc. All Rights Reserved.

Licensed under the MIT License (the "License"). You may not use this work except in compliance with the License. You may obtain a copy of the License in the LICENSE file.
