package util

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	rspecs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/opencontainers/runtime-tools/generate"
	"github.com/opencontainers/runtime-tools/specerror"
)

// Runtime represents the basic requirement of a container runtime
type Runtime struct {
	RuntimeCommand string
	BundleDir      string
	PidFile        string
	ID             string
	stdout         bytes.Buffer
	stderr         bytes.Buffer
}

// DefaultSignal represents the default signal sends to a container
const DefaultSignal = "TERM"

// NewRuntime create a runtime by command and the bundle directory
func NewRuntime(runtimeCommand string, bundleDir string) (Runtime, error) {
	var r Runtime
	var err error
	r.RuntimeCommand, err = exec.LookPath(runtimeCommand)
	if err != nil {
		return Runtime{}, err
	}

	r.BundleDir = bundleDir
	return r, err
}

// bundleDir returns the bundle directory.  Generally this is
// BundleDir, but when BundleDir is the empty string, it falls back to
// ., as specified in the CLI spec.
func (r *Runtime) bundleDir() (bundleDir string) {
	if r.BundleDir == "" {
		return "."
	}
	return r.BundleDir
}

// SetConfig creates a 'config.json' by the generator
func (r *Runtime) SetConfig(g *generate.Generator) error {
	if g == nil {
		return errors.New("cannot set a nil config")
	}
	return g.SaveToFile(filepath.Join(r.bundleDir(), "config.json"), generate.ExportOptions{})
}

// SetID sets the container ID
func (r *Runtime) SetID(id string) {
	r.ID = id
}

// Create a container
func (r *Runtime) Create() (err error) {
	var args []string
	args = append(args, "create")
	if r.PidFile != "" {
		args = append(args, "--pid-file", r.PidFile)
	}
	if r.BundleDir != "" {
		args = append(args, "--bundle", r.BundleDir)
	}
	if r.ID != "" {
		args = append(args, r.ID)
	}
	cmd := exec.Command(r.RuntimeCommand, args...)
	r.stdout.Reset()
	cmd.Stdout = &r.stdout
	r.stderr.Reset()
	cmd.Stderr = &r.stderr

	err = cmd.Run()
	if err == nil {
		return err
	}

	if e, ok := err.(*exec.ExitError); ok {
		stdout, stderr := r.StandardStreams()
		if len(stderr) == 0 {
			stderr = stdout
		}
		e.Stderr = stderr
		return e
	}
	return err
}

// StandardStreams returns content from the stdout and stderr buffers.
func (r *Runtime) StandardStreams() (stdout, stderr []byte) {
	return r.stdout.Bytes(), r.stderr.Bytes()
}

// Start a container
func (r *Runtime) Start() (err error) {
	var args []string
	args = append(args, "start")
	if r.ID != "" {
		args = append(args, r.ID)
	}

	cmd := exec.Command(r.RuntimeCommand, args...)
	return execWithStderrFallbackToStdout(cmd)
}

// State a container information
func (r *Runtime) State() (rspecs.State, error) {
	var args []string
	args = append(args, "state")
	if r.ID != "" {
		args = append(args, r.ID)
	}

	out, err := exec.Command(r.RuntimeCommand, args...).Output()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			if len(e.Stderr) == 0 {
				e.Stderr = out
				return rspecs.State{}, e
			}
		}
		return rspecs.State{}, err
	}

	var state rspecs.State
	err = json.Unmarshal(out, &state)
	if err != nil {
		return rspecs.State{}, specerror.NewError(specerror.DefaultStateJSONPattern, fmt.Errorf("when serialized in JSON, the format MUST adhere to the default pattern"), rspecs.Version)
	}
	return state, err
}

// Kill a container
func (r *Runtime) Kill(sig string) (err error) {
	var args []string
	args = append(args, "kill")
	if r.ID != "" {
		args = append(args, r.ID)
	}
	if sig != "" {
		// TODO: runc does not support this
		//	args = append(args, "--signal", sig)
		args = append(args, sig)
	} else {
		args = append(args, DefaultSignal)
	}

	cmd := exec.Command(r.RuntimeCommand, args...)
	return execWithStderrFallbackToStdout(cmd)
}

// Delete a container
func (r *Runtime) Delete() (err error) {
	var args []string
	args = append(args, "delete")
	if r.ID != "" {
		args = append(args, r.ID)
	}

	cmd := exec.Command(r.RuntimeCommand, args...)
	return execWithStderrFallbackToStdout(cmd)
}

// Clean deletes the container.  If removeBundle is set, the bundle
// directory is removed after the container is deleted successfully or, if
// forceRemoveBundle is true, after the deletion attempt regardless of
// whether it was successful or not.
func (r *Runtime) Clean(removeBundle bool, forceRemoveBundle bool) {
	if err := r.Kill("KILL"); err != nil {
		fmt.Fprintf(os.Stderr, "Clean: Kill: %v", err)
	}
	if err := WaitingForStatus(*r, LifecycleStatusStopped, time.Second*10, time.Second/10); err != nil {
		fmt.Fprintf(os.Stderr, "Clean: %v", err)
	}

	err := r.Delete()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Clean: Delete: %v", err)
	}

	if removeBundle && (err == nil || forceRemoveBundle) {
		err := os.RemoveAll(r.bundleDir())
		if err != nil {
			fmt.Fprintf(os.Stderr, "Clean: %v", err)
		}
	}
}

func execWithStderrFallbackToStdout(cmd *exec.Cmd) (err error) {
	stdout, err := cmd.Output()
	if err == nil {
		return err
	}

	if e, ok := err.(*exec.ExitError); ok {
		if len(e.Stderr) == 0 {
			e.Stderr = stdout
			return e
		}
	}
	return err
}
