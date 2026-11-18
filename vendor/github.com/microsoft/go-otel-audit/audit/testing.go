package audit

import (
	"github.com/microsoft/go-otel-audit/audit/msgs"
)

type newSenderResp struct {
	sender msgSenderer
	err    error
}

// testParams provides values for faking various method calls.
type testParams struct {
	// serviceMsgVals is a slice of pairs of (stopped, error) values that the serviceMsg function will return.
	// This is used to simulate different scenarios in tests.
	serviceMsgVals []error           // Each element is a pair of (stopped, error)
	writerVals     []error           // Errors to return from the writer function
	writeCount     map[msgs.Type]int // Count of how many times each message type has been written
	senders        []newSenderResp   // List of senders to return from the newSender function
}

// serviceMsg simulates the service message function for testing purposes.
func (t *testParams) serviceMsg() error {
	if len(t.serviceMsgVals) == 0 {
		return nil
	}
	err := t.serviceMsgVals[0]
	t.serviceMsgVals = t.serviceMsgVals[1:]
	return err
}

// writer simulates the writer function for testing purposes.
func (t *testParams) writer(msg msgs.Msg) error {
	if len(t.writerVals) == 0 {
		panic("no more writer values available")
	}
	err := t.writerVals[0]
	t.writerVals = t.writerVals[1:]
	if err != nil {
		return err
	}

	if t.writeCount == nil {
		t.writeCount = make(map[msgs.Type]int)
	}
	t.writeCount[msg.Type]++

	return nil
}

func (t *testParams) newSender() (msgSenderer, error) {
	if len(t.senders) == 0 {
		panic("no more senders available")
	}
	resp := t.senders[0]
	t.senders = t.senders[1:]
	return resp.sender, resp.err
}

// mustNewMetrics creates metrics for testing. Panics on error since the default
// OTel noop meter provider never fails.
func mustNewMetrics() *metrics {
	m, err := newMetrics()
	if err != nil {
		panic("mustNewMetrics: " + err.Error())
	}
	return m
}
