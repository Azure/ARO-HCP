package openshift_test_integration

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/onsi/ginkgo/v2/types"
	"github.com/openshift-eng/openshift-tests-extension/pkg/dbtime"
	"github.com/openshift-eng/openshift-tests-extension/pkg/extension/extensiontests"
	"k8s.io/utils/ptr"
)

func ProcessExecRunFactory(testName string, spec types.TestSpec) func() *extensiontests.ExtensionTestResult {
	return func() *extensiontests.ExtensionTestResult {
		return SpawnProcessToRunTest(context.TODO(), testName, extensiontests.LifecycleBlocking, 90*time.Minute)
	}
}

func SpawnProcessToRunTest(ctx context.Context, testName string, testLifecycle extensiontests.Lifecycle, timeout time.Duration) *extensiontests.ExtensionTestResult {
	fmt.Printf("Running test %q\n", testName)

	// longerCtx is used to backstop the process, but leave termination up to us if possible to allow a double interrupt
	longerCtx, longerCancel := context.WithTimeout(ctx, timeout+15*time.Minute)
	defer longerCancel()
	timeoutCtx, shorterCancel := context.WithTimeout(longerCtx, timeout)
	defer shorterCancel()

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	command := exec.CommandContext(longerCtx, os.Args[0], "run-test", testName)
	command.Stdout = stdout
	command.Stderr = stderr

	start := time.Now()
	fmt.Printf("Starting test %q with command %s\n", testName, command.String())
	err := command.Start()
	if err != nil {
		fmt.Fprintf(stderr, "failed to start test: %v\n", err)
		return newTestResult(testName, extensiontests.ResultFailed, testLifecycle, start, time.Now(), stdout, stderr)
	}

	go func() {
		select {
		// interrupt tests after timeout, and abort if they don't complete quick enough
		case <-time.After(timeout):
			if command.Process != nil {
				command.Process.Signal(syscall.SIGINT)
			}
			// if the process appears to be hung a significant amount of time after the timeout
			// send an ABRT so we get a stack dump
			select {
			case <-time.After(time.Minute):
				if command.Process != nil {
					command.Process.Signal(syscall.SIGABRT)
				}
			}
		case <-timeoutCtx.Done():
			if command.Process != nil {
				command.Process.Signal(syscall.SIGINT)
			}
		}
	}()

	result := extensiontests.ResultFailed
	var exitError *exec.ExitError
	fmt.Printf("Waiting for test %q to complete\n", testName)
	cmdErr := command.Wait()
	switch {
	case cmdErr == nil:
		result = extensiontests.ResultPassed
	case errors.As(err, &exitError):
		switch exitError.ProcessState.Sys().(syscall.WaitStatus).ExitStatus() {
		case 1: // failed
			result = extensiontests.ResultFailed
		case 2: // TODO timeout (ABRT is an exit code 2)
			result = extensiontests.ResultFailed
		case 3: // skipped
			result = extensiontests.ResultSkipped
		case 4: // TODO flaky, do not retry maybe provisional?
			result = extensiontests.ResultFailed
		default:
			result = extensiontests.ResultFailed
		}
	default:
		fmt.Fprintf(stderr, "\nProcess Failed: %v\n", err)
		result = extensiontests.ResultFailed
	}

	fmt.Printf("Test %q completed with result %s\n", testName, result)
	return newTestResult(testName, result, testLifecycle, start, time.Now(), stdout, stderr)
}

func newTestResult(name string, result extensiontests.Result, lifecycle extensiontests.Lifecycle, start, end time.Time, stdout, stderr *bytes.Buffer) *extensiontests.ExtensionTestResult {
	duration := end.Sub(start)
	ret := &extensiontests.ExtensionTestResult{
		Name:      name,
		Lifecycle: lifecycle,
		Duration:  int64(duration),
		StartTime: ptr.To(dbtime.DBTime(start)),
		EndTime:   ptr.To(dbtime.DBTime(end)),
		Result:    result,
		Details:   nil,
	}

	if stdout != nil && stderr != nil {
		stdoutStr := stdout.String()
		stderrStr := stderr.String()

		ret.Output = fmt.Sprintf("STDOUT:\n%s\n\nSTDERR:\n%s\n", stdoutStr, stderrStr)

		// try to choose the best summary
		switch {
		case len(stderrStr) > 0 && len(stderrStr) < 5000:
			ret.Error = stderrStr
		case len(stderrStr) > 0 && len(stderrStr) >= 5000:
			ret.Error = stderrStr[len(stderrStr)-5000:]

		case len(stdoutStr) > 0 && len(stdoutStr) < 5000:
			ret.Error = stdoutStr
		case len(stdoutStr) > 0 && len(stdoutStr) >= 5000:
			ret.Error = stdoutStr[len(stdoutStr)-5000:]
		}
	}

	return ret
}
