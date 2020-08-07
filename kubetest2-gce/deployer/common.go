/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package deployer

import (
	"fmt"
	"os"
	"time"

	"k8s.io/klog"
	"sigs.k8s.io/kubetest2/pkg/boskos"
)

const (
	gceProjectResourceType = "gce-project"
)

func (d *deployer) init() error {
	var err error
	d.doInit.Do(func() { err = d.initialize() })
	return err
}

// initialize should only be called by init(), behind a sync.Once
func (d *deployer) initialize() error {
	if d.commonOptions.ShouldBuild() {
		if err := d.verifyBuildFlags(); err != nil {
			return fmt.Errorf("init failed to check build flags: %s", err)
		}
	}

	if d.commonOptions.ShouldUp() {
		if err := d.verifyUpFlags(); err != nil {
			return fmt.Errorf("init failed to verify flags for up: %s", err)
		}

		if d.GCPProject == "" {
			klog.V(1).Info("No GCP project provided, acquiring from Boskos")

			boskosClient, err := boskos.NewClient(d.BoskosLocation)
			if err != nil {
				return fmt.Errorf("failed to make boskos client: %s", err)
			}
			d.boskos = boskosClient

			resource, err := boskos.Acquire(
				d.boskos,
				gceProjectResourceType,
				time.Duration(d.BoskosAcquireTimeoutSeconds)*time.Second,
				d.boskosHeartbeatClose,
			)

			if err != nil {
				return fmt.Errorf("init failed to get project from boskos: %s", err)
			}
			d.GCPProject = resource.Name
			klog.V(1).Infof("Got project %s from boskos", d.GCPProject)
		}

	}

	if d.commonOptions.ShouldDown() {
		if err := d.verifyDownFlags(); err != nil {
			return fmt.Errorf("init failed to verify flags for down: %s", err)
		}
	}

	return nil
}

func (d *deployer) buildEnv() []string {
	// The base env currently does not inherit the current os env (except for PATH)
	// because (for now) it doesn't have to. In future, this may have to change when
	// support is added for k/k's kube-up.sh and kube-down.sh which support a wide
	// variety of environment variables. Before doing so, it is worth investigating
	// inheriting the os env vs. adding flags to this deployer on a case-by-case
	// basis to support individual environment configurations.
	var env []string

	// path is necessary for scripts to find gsutil, gcloud, etc
	// can be removed if env is inherited from the os
	env = append(env, fmt.Sprintf("PATH=%s", os.Getenv("PATH")))

	// USER is used by config-test.sh to set $NETWORK in the default case.
	// Also, if unset, bash's set -u gets angry and kills the log dump script.
	//
	// Because the log dump script uses `gcloud compute ssh` and
	// `gcloud compute scp`, we have to check if the active user is root.
	// This is because `gcloud compute ssh/scp` try to log in as USER@vm
	// which is by default disabled on GCE VMs if USER is root. In order
	// for the deployer to work without fuss when run as root (like it
	// does by default in Prow) we can simply change USER to be something
	// non-root. USER is not always set in a given environment, so the UID
	// is checked instead for guaranteed correct information.
	if uid := os.Getuid(); uid == 0 {
		env = append(env, fmt.Sprintf("USER=%s", "kubetest2"))
	} else {
		env = append(env, fmt.Sprintf("USER=%s", os.Getenv("USER")))
	}

	// kube-up.sh, kube-down.sh etc. use PROJECT as a parameter
	// for gcloud commands
	env = append(env, fmt.Sprintf("PROJECT=%s", d.GCPProject))

	// KUBE_GCE_ZONE is used by up and down scripts. It is used mainly
	// to set the ZONE var, which can't be set directly here because it
	// will be overridden when the scripts check KUBE_GCE_ZONE.
	env = append(env, fmt.Sprintf("KUBE_GCE_ZONE=%s", d.GCPZone))

	// kubeconfig is set to tell kube-up.sh where to generate the kubeconfig
	// we don't want this to be the default because this kubeconfig "belongs" to
	// the run of kubetest2 and so should be placed in the artifacts directory
	env = append(env, fmt.Sprintf("KUBECONFIG=%s", d.kubeconfigPath))

	// kube-up and kube-down get this as a default ("kubernetes") but log-dump
	// does not. opted to set it manually here for maximum consistency
	env = append(env, "KUBE_GCE_INSTANCE_PREFIX=kubetest2")

	// Pass through number of nodes and associated IP range. In the future,
	// IP range will be configurable.
	env = append(env, fmt.Sprintf("NUM_NODES=%d", d.NumNodes))
	env = append(env, fmt.Sprintf("CLUSTER_IP_RANGE=%s", getClusterIPRange(d.NumNodes)))

	if d.EnableCacheMutationDetector {
		env = append(env, "ENABLE_CACHE_MUTATION_DETECTOR=true")
	}

	if d.RuntimeConfig != "" {
		env = append(env, fmt.Sprintf("KUBE_RUNTIME_CONFIG=%s", d.RuntimeConfig))
	}

	return env
}

// Taken from the kubetest bash (gce) deployer
// Calculates the cluster IP range based on the no. of nodes in the cluster.
// Note: This mimics the function get-cluster-ip-range used by kube-up script.
func getClusterIPRange(numNodes int) string {
	suggestedRange := "10.64.0.0/14"
	if numNodes > 1000 {
		suggestedRange = "10.64.0.0/13"
	}
	if numNodes > 2000 {
		suggestedRange = "10.64.0.0/12"
	}
	if numNodes > 4000 {
		suggestedRange = "10.64.0.0/11"
	}
	return suggestedRange
}
