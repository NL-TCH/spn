package terminal

import (
	"fmt"
	"os"
	"runtime/pprof"
	"sync/atomic"
	"testing"
	"time"

	"github.com/safing/portbase/container"
	"github.com/safing/spn/cabin"
)

func TestTerminals(t *testing.T) {
	t.Parallel()

	var testQueueSize uint32 = defaultTestQueueSize
	countToQueueSize := uint64(testQueueSize)

	initMsg := &TerminalOpts{
		QueueSize: testQueueSize,
		Padding:   defaultTestPadding,
	}

	var term1 *TestTerminal
	var term2 *TestTerminal
	var initData *container.Container
	var err *Error
	term1, initData, err = NewLocalTestTerminal(
		module.Ctx, 127, "c1", nil, initMsg, createTestForwardingFunc(
			t, "c1", "c2", func(c *container.Container) *Error {
				return term2.DuplexFlowQueue.Deliver(c)
			},
		),
	)
	if err != nil {
		t.Fatalf("failed to create local terminal: %s", err)
	}
	term2, _, err = NewRemoteTestTerminal(
		module.Ctx, 127, "c2", nil, initData, createTestForwardingFunc(
			t, "c2", "c1", func(c *container.Container) *Error {
				return term1.DuplexFlowQueue.Deliver(c)
			},
		),
	)
	if err != nil {
		t.Fatalf("failed to create remote terminal: %s", err)
	}

	// Start testing with counters.

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlyup-flushing-waiting",
		flush:           true,
		serverCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: sendThresholdMaxWait * 2,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlyup-waiting",
		serverCountTo:   10,
		waitBetweenMsgs: sendThresholdMaxWait * 2,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlyup-flushing",
		flush:           true,
		serverCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: time.Millisecond,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlyup",
		serverCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: time.Millisecond,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlydown-flushing-waiting",
		flush:           true,
		clientCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: sendThresholdMaxWait * 2,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlydown-waiting",
		clientCountTo:   10,
		waitBetweenMsgs: sendThresholdMaxWait * 2,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlydown-flushing",
		flush:           true,
		clientCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: time.Millisecond,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "onlydown",
		clientCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: time.Millisecond,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "twoway-flushing-waiting",
		flush:           true,
		clientCountTo:   countToQueueSize * 2,
		serverCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: sendThresholdMaxWait * 2,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "twoway-waiting",
		flush:           true,
		clientCountTo:   10,
		serverCountTo:   10,
		waitBetweenMsgs: sendThresholdMaxWait * 2,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "twoway-flushing",
		flush:           true,
		clientCountTo:   countToQueueSize * 2,
		serverCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: time.Millisecond,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "twoway",
		clientCountTo:   countToQueueSize * 2,
		serverCountTo:   countToQueueSize * 2,
		waitBetweenMsgs: time.Millisecond,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:      "stresstest-down",
		clientCountTo: countToQueueSize * 1000,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:      "stresstest-up",
		serverCountTo: countToQueueSize * 1000,
	})

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:      "stresstest-duplex",
		clientCountTo: countToQueueSize * 1000,
		serverCountTo: countToQueueSize * 1000,
	})
}

func TestTerminalsWithEncryption(t *testing.T) {
	t.Parallel()

	var testQueueSize uint32 = defaultTestQueueSize
	countToQueueSize := uint64(testQueueSize)

	initMsg := &TerminalOpts{
		QueueSize: testQueueSize,
		Padding:   8,
	}

	identity, erro := cabin.CreateIdentity(module.Ctx, "test")
	if erro != nil {
		t.Fatalf("failed to create identity: %s", erro)
	}

	var term1 *TestTerminal
	var term2 *TestTerminal
	var initData *container.Container
	var err *Error
	term1, initData, err = NewLocalTestTerminal(
		module.Ctx, 127, "c1", identity.Hub, initMsg, createTestForwardingFunc(
			t, "c1", "c2", func(c *container.Container) *Error {
				return term2.DuplexFlowQueue.Deliver(c)
			},
		),
	)
	if err != nil {
		t.Fatalf("failed to create local terminal: %s", err)
	}
	term2, _, err = NewRemoteTestTerminal(
		module.Ctx, 127, "c2", identity, initData, createTestForwardingFunc(
			t, "c2", "c1", func(c *container.Container) *Error {
				return term1.DuplexFlowQueue.Deliver(c)
			},
		),
	)
	if err != nil {
		t.Fatalf("failed to create remote terminal: %s", err)
	}

	testTerminalWithCounters(t, term1, term2, &testWithCounterOpts{
		testName:        "twoway-encrypting",
		clientCountTo:   countToQueueSize * 20,
		serverCountTo:   countToQueueSize * 20,
		waitBetweenMsgs: time.Millisecond,
	})
}

func createTestForwardingFunc(t *testing.T, srcName, dstName string, deliverFunc func(*container.Container) *Error) func(*container.Container) {
	t.Helper()

	return func(c *container.Container) {
		// Fast track nil containers.
		if c == nil {
			dErr := deliverFunc(c)
			if dErr != nil {
				t.Errorf("%s>%s: failed to deliver nil msg to terminal: %s", srcName, dstName, dErr)
			}
			return
		}

		// Log messages.
		if logTestCraneMsgs {
			t.Logf("%s>%s: %v\n", srcName, dstName, c.CompileData())
		}

		// Deliver to other terminal.
		dErr := deliverFunc(c)
		if dErr != nil {
			t.Errorf("%s>%s: failed to deliver to terminal: %s", srcName, dstName, dErr)
		}
	}
}

type testWithCounterOpts struct {
	testName        string
	flush           bool
	clientCountTo   uint64
	serverCountTo   uint64
	waitBetweenMsgs time.Duration
}

func testTerminalWithCounters(t *testing.T, term1, term2 *TestTerminal, opts *testWithCounterOpts) {
	t.Helper()

	// Wait async for test to complete, print stack after timeout.
	finished := make(chan struct{})
	go func() {
		select {
		case <-finished:
		case <-time.After(60 * time.Second):
			fmt.Printf("terminal test %s is taking too long, print stack:\n", opts.testName)
			_ = pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
			os.Exit(1)
		}
	}()

	t.Logf("starting terminal counter test %s", opts.testName)
	defer t.Logf("stopping terminal counter test %s", opts.testName)

	// Start counters.
	counter, tErr := NewCounterOp(term1, CounterOpts{
		ClientCountTo: opts.clientCountTo,
		ServerCountTo: opts.serverCountTo,
		Flush:         opts.flush,
		Wait:          opts.waitBetweenMsgs,
	})
	if tErr != nil {
		t.Fatalf("terminal test %s failed to start counter: %s", opts.testName, tErr)
	}

	// Wait until counters are done.
	counter.Wait()
	close(finished)

	// Check for error.
	if counter.Error != nil {
		t.Fatalf("terminal test %s failed to count: %s", opts.testName, counter.Error)
	}

	// Log stats.
	printCTStats(t, opts.testName, "term1", term1)
	printCTStats(t, opts.testName, "term2", term2)

	// Check if stats match.
	if atomic.LoadInt32(term1.DuplexFlowQueue.sendSpace) != atomic.LoadInt32(term2.DuplexFlowQueue.reportedSpace) ||
		atomic.LoadInt32(term2.DuplexFlowQueue.sendSpace) != atomic.LoadInt32(term1.DuplexFlowQueue.reportedSpace) {
		t.Fatalf("terminal test %s has non-matching space counters", opts.testName)
	}
}

func printCTStats(t *testing.T, testName, name string, term *TestTerminal) {
	t.Helper()
	t.Logf(
		"%s: %s: sq=%d rq=%d sends=%d reps=%d opq=%d",
		testName,
		name,
		len(term.DuplexFlowQueue.sendQueue),
		len(term.DuplexFlowQueue.recvQueue),
		atomic.LoadInt32(term.DuplexFlowQueue.sendSpace),
		atomic.LoadInt32(term.DuplexFlowQueue.reportedSpace),
		len(term.opMsgQueue),
	)
}
