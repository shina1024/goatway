package throttle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// Fetcher supplies the current deployment and traffic state to the poller.
type Fetcher interface {
	FetchInstanceCounts(ctx context.Context) (InstanceCounts, error)
	FetchTrafficWeight(ctx context.Context) (TrafficWeight, error)
}

// FileFetcher reads local deployment state instead of Kubernetes and Istio.
type FileFetcher struct {
	mu                  sync.Mutex
	path                string
	cachedDeployment    deploymentFile
	hasCachedDeployment bool
}

type deploymentFile struct {
	PrimaryPods   int `yaml:"primary_pods"`
	CanaryPods    int `yaml:"canary_pods"`
	PrimaryWeight int `yaml:"primary_weight"`
	CanaryWeight  int `yaml:"canary_weight"`
}

// NewFileFetcher creates a fetcher that reads path once for each count and weight pair.
func NewFileFetcher(path string) *FileFetcher {
	return &FileFetcher{path: path}
}

// FetchInstanceCounts returns the current primary and canary pod counts.
func (f *FileFetcher) FetchInstanceCounts(ctx context.Context) (InstanceCounts, error) {
	if f == nil {
		return InstanceCounts{}, &FetchError{Err: errors.New("nil file fetcher")}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.cachedDeployment = deploymentFile{}
	f.hasCachedDeployment = false

	deployment, err := f.fetchDeployment(ctx)
	if err != nil {
		return InstanceCounts{}, err
	}
	f.cachedDeployment = deployment
	f.hasCachedDeployment = true
	return InstanceCounts{Primary: deployment.PrimaryPods, Canary: deployment.CanaryPods}, nil
}

// FetchTrafficWeight returns the current primary and canary traffic weights.
func (f *FileFetcher) FetchTrafficWeight(ctx context.Context) (TrafficWeight, error) {
	if f == nil {
		return TrafficWeight{}, &FetchError{Err: errors.New("nil file fetcher")}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hasCachedDeployment {
		if err := ctx.Err(); err != nil {
			return TrafficWeight{}, &FetchError{Err: fmt.Errorf("deployment file context: %w", err)}
		}
		deployment := f.cachedDeployment
		f.cachedDeployment = deploymentFile{}
		f.hasCachedDeployment = false
		return TrafficWeight{Primary: deployment.PrimaryWeight, Canary: deployment.CanaryWeight}, nil
	}

	deployment, err := f.fetchDeployment(ctx)
	if err != nil {
		return TrafficWeight{}, err
	}
	return TrafficWeight{Primary: deployment.PrimaryWeight, Canary: deployment.CanaryWeight}, nil
}

// FileFetcher cannot observe Kubernetes pod termination, so it never returns a TerminatingError.
func (f *FileFetcher) fetchDeployment(ctx context.Context) (deploymentFile, error) {
	if f == nil {
		return deploymentFile{}, &FetchError{Err: errors.New("nil file fetcher")}
	}
	if err := ctx.Err(); err != nil {
		return deploymentFile{}, &FetchError{Err: fmt.Errorf("deployment file context: %w", err)}
	}

	data, err := os.ReadFile(f.path)
	if err != nil {
		return deploymentFile{}, &FetchError{Err: fmt.Errorf("read deployment file %q: %w", f.path, err)}
	}

	var deployment deploymentFile
	if err := yaml.Unmarshal(data, &deployment); err != nil {
		return deploymentFile{}, &FetchError{Err: fmt.Errorf("parse deployment file %q: %w", f.path, err)}
	}
	return deployment, nil
}
