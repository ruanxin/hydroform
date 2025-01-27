package terraform

import (
	"io/ioutil"
	"log"
	"os"

	"github.com/kyma-incubator/hydroform/provision/types"
	"github.com/pkg/errors"

	"github.com/hashicorp/terraform/states/statefile"
)

// Terraform is an Operator.
type Terraform struct {
	ops Options
}

// New creates a new Terraform operator with the given options
func New(ops ...Option) *Terraform {
	// silence the logs since terraform prints a lot of stuff
	tfOps := options(ops...)
	if !tfOps.Verbose {
		log.SetOutput(ioutil.Discard)
	}

	return &Terraform{
		ops: tfOps,
	}
}

// Create creates a new cluster for a specific provider based on configuration details. It returns a ClusterInfo object with provider-related information, or an error if cluster provisioning failed.
func (t *Terraform) Create(p types.ProviderType, cfg map[string]interface{}) (*types.ClusterInfo, error) {
	applyTimeouts(cfg, t.ops.Timeouts)

	// silence stdErr during terraform execution, plugins send debug and trace entries there
	if !t.ops.Verbose {
		stderr := os.Stderr
		var err error
		os.Stderr, err = os.Open(os.DevNull)
		if err != nil {
			return nil, err
		}
		defer func() { os.Stderr = stderr }()
	}

	// init cluster files
	if !t.ops.Persistent {
		// remove all files if not persistent after running
		defer cleanup(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
	}

	clusterDir, err := clusterDir(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
	if err != nil {
		return nil, err
	}
	// INIT
	if p == types.Gardener {
		if err := initGardenerProvider(); err != nil {
			return nil, errors.Wrap(err, "could not initialize the gardener provider")
		}
	}
	if err := tfInit(t.ops, p, cfg, clusterDir); err != nil {
		return nil, err
	}

	if err := initClusterFiles(t.ops.DataDir(), p, cfg); err != nil {
		return nil, errors.Wrap(err, "Could not initialize cluster data")
	}

	// APPLY
	if err := tfApply(t.ops, p, cfg, clusterDir); err != nil {
		return nil, err
	}
	return clusterInfoFromFile(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
}

// Status checks the current state of the cluster from the file
func (t *Terraform) Status(sf *statefile.File, p types.ProviderType, cfg map[string]interface{}) (*types.ClusterStatus, error) {
	applyTimeouts(cfg, t.ops.Timeouts)

	cs := &types.ClusterStatus{
		Phase: types.Unknown,
	}
	var err error

	// if no state given, try the file system
	if sf == nil {
		sf, err = stateFromFile(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
		if err != nil {
			return cs, errors.Wrap(err, "no state provided, attempted to load from file")
		}
	}

	if sf.State.HasResources() {
		cs.Phase = types.Provisioned
	}

	return cs, nil
}

// Delete removes an existing cluster or returns an error if removing the cluster is not possible.
func (t *Terraform) Delete(sf *statefile.File, p types.ProviderType, cfg map[string]interface{}) error {
	applyTimeouts(cfg, t.ops.Timeouts)

	// silence stdErr during terraform execution, plugins send debug and trace entries there
	stderr := os.Stderr
	var err error
	os.Stderr, err = os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer func() { os.Stderr = stderr }()

	// init cluster files
	if !t.ops.Persistent {
		// remove all files if not persistent after running
		defer cleanup(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
	}

	clusterDir, err := clusterDir(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
	if err != nil {
		return err
	}

	// INIT
	if p == types.Gardener {
		if err := initGardenerProvider(); err != nil {
			return errors.Wrap(err, "could not initialize the gardener provider")
		}
	}
	if err := tfInit(t.ops, p, cfg, clusterDir); err != nil {
		return err
	}
	if err := initClusterFiles(t.ops.DataDir(), p, cfg); err != nil {
		return errors.Wrap(err, "Could not initialize cluster data")
	}

	// if no state given, check if it is already in the file system
	if sf == nil {
		_, err := stateFromFile(t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p)
		if err != nil {
			return errors.Wrap(err, "no state provided, attempted to load from file")
		}
	} else {
		// otherwise save the state into a file so terraform can use it
		if err := stateToFile(sf, t.ops.DataDir(), cfg["project"].(string), cfg["cluster_name"].(string), p); err != nil {
			return errors.Wrap(err, "could not store state into file")
		}
	}

	// APPLY
	if err := tfDestroy(t.ops, p, cfg, clusterDir); err != nil {
		return err
	}
	return nil
}
