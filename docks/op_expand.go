package docks

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/tevino/abool"

	"github.com/safing/portbase/container"
	"github.com/safing/spn/conf"
	"github.com/safing/spn/terminal"
)

// ExpandOpType is the type ID of the expand operation.
const ExpandOpType string = "expand"

var activeExpandOps = new(int64)

// ExpandOp is used to expand to another Hub.
type ExpandOp struct {
	terminal.OpBase

	opTerminal terminal.OpTerminal
	*terminal.DuplexFlowQueue

	// ctx is the context of the Terminal.
	ctx context.Context
	// cancelCtx cancels ctx.
	cancelCtx context.CancelFunc

	dataRelayed *uint64
	ended       *abool.AtomicBool

	relayTerminal *ExpansionRelayTerminal
}

// ExpansionRelayTerminal is a relay used for expansion.
type ExpansionRelayTerminal struct {
	*terminal.DuplexFlowQueue
	op *ExpandOp

	id    uint32
	crane *Crane

	abandoned *abool.AtomicBool
}

// Type returns the type ID.
func (op *ExpandOp) Type() string {
	return ExpandOpType
}

// ID returns the operation ID.
func (t *ExpansionRelayTerminal) ID() uint32 {
	return t.id
}

// Ctx returns the operation context.
func (op *ExpandOp) Ctx() context.Context {
	return op.ctx
}

// Ctx returns the relay terminal context.
func (t *ExpansionRelayTerminal) Ctx() context.Context {
	return t.op.ctx
}

func init() {
	terminal.RegisterOpType(terminal.OpParams{
		Type:     ExpandOpType,
		Requires: terminal.MayExpand,
		RunOp:    expand,
	})
}

func expand(t terminal.OpTerminal, opID uint32, data *container.Container) (terminal.Operation, *terminal.Error) {
	// Submit metrics.
	newExpandOp.Inc()

	// Check if we are running a public hub.
	if !conf.PublicHub() {
		return nil, terminal.ErrPermissinDenied.With("expanding is only allowed on public hubs")
	}

	// Parse destination hub ID.
	dstData, err := data.GetNextBlock()
	if err != nil {
		return nil, terminal.ErrMalformedData.With("failed to parse destination: %w", err)
	}

	// Parse terminal options.
	opts, tErr := terminal.ParseTerminalOpts(data)
	if tErr != nil {
		return nil, tErr.Wrap("failed to parse terminal options")
	}

	// Get crane with destination.
	relayCrane := GetAssignedCrane(string(dstData))
	if relayCrane == nil {
		return nil, terminal.ErrHubUnavailable.With("no crane assigned to %q", string(dstData))
	}

	// TODO: Expand outside of hot path.

	// Create operation and terminal.
	op := &ExpandOp{
		opTerminal:  t,
		dataRelayed: new(uint64),
		ended:       abool.New(),
		relayTerminal: &ExpansionRelayTerminal{
			crane:     relayCrane,
			id:        relayCrane.getNextTerminalID(),
			abandoned: abool.New(),
		},
	}
	op.OpBase.Init()
	op.OpBase.SetID(opID)
	op.ctx, op.cancelCtx = context.WithCancel(context.Background())
	op.relayTerminal.op = op
	// Create flow queues.
	op.DuplexFlowQueue = terminal.NewDuplexFlowQueue(op, opts.QueueSize, op.submitBackstream)
	op.relayTerminal.DuplexFlowQueue = terminal.NewDuplexFlowQueue(op, opts.QueueSize, op.submitForwardstream)

	// Establish terminal on destination.
	newInitData, tErr := opts.Pack()
	if tErr != nil {
		return nil, terminal.ErrInternalError.With("failed to re-pack options: %w", err)
	}
	tErr = op.relayTerminal.crane.EstablishNewTerminal(op.relayTerminal, newInitData)
	if tErr != nil {
		return nil, tErr
	}

	// Start workers.
	module.StartWorker("expand op flow", op.DuplexFlowQueue.FlowHandler)
	module.StartWorker("expand op terminal flow", op.relayTerminal.DuplexFlowQueue.FlowHandler)
	module.StartWorker("expand op forward relay", op.forwardHandler)
	module.StartWorker("expand op backward relay", op.backwardHandler)

	return op, nil
}

func (op *ExpandOp) submitForwardstream(c *container.Container) {
	terminal.MakeMsg(c, op.relayTerminal.id, terminal.MsgTypeData)
	op.relayTerminal.crane.submitTerminalMsg(c)
}

func (op *ExpandOp) submitBackstream(c *container.Container) {
	err := op.opTerminal.OpSend(op, c)
	if err != nil {
		op.opTerminal.OpEnd(op, err.Wrap("failed to send from relay op"))
	}
}

func (op *ExpandOp) forwardHandler(_ context.Context) error {
	// Metrics setup and submitting.
	atomic.AddInt64(activeExpandOps, 1)
	started := time.Now()
	defer func() {
		atomic.AddInt64(activeExpandOps, -1)
		expandOpDurationHistogram.UpdateDuration(started)
		expandOpRelayedDataHistogram.Update(float64(atomic.LoadUint64(op.dataRelayed)))
	}()

	for {
		select {
		case c := <-op.DuplexFlowQueue.Receive():
			// Debugging:
			// log.Debugf("forwarding at %s: %s", op.FmtID(), spew.Sdump(c.CompileData()))

			// Count relayed data for metrics.
			atomic.AddUint64(op.dataRelayed, uint64(c.Length()))

			// Receive data from the origin and forward it to the relay.
			if err := op.relayTerminal.DuplexFlowQueue.Send(c); err != nil {
				return err
			}

		case <-op.ctx.Done():
			return nil
		}
	}
}

func (op *ExpandOp) backwardHandler(_ context.Context) error {
	for {
		select {
		case c := <-op.relayTerminal.DuplexFlowQueue.Receive():
			// Debugging:
			// log.Debugf("backwarding at %s: %s", op.FmtID(), spew.Sdump(c.CompileData()))

			// Count relayed data for metrics.
			atomic.AddUint64(op.dataRelayed, uint64(c.Length()))

			// Receive data from the relay and forward it to the origin.
			if err := op.DuplexFlowQueue.Send(c); err != nil {
				return err
			}

		case <-op.ctx.Done():
			return nil
		}
	}
}

// Abandon abandons the expansion.
func (op *ExpandOp) Abandon(err *terminal.Error) {
	// Proxy for Terminal Interface needed for the Duplex Flow Queue.
	op.End(err)
}

// End abandons the expansion.
func (op *ExpandOp) End(err *terminal.Error) {
	if op.ended.SetToIf(false, true) {
		// Init proper process.
		op.opTerminal.OpEnd(op, err)

		// Stop connected workers.
		op.cancelCtx()

		// Abandon connected terminal.
		op.relayTerminal.crane.AbandonTerminal(op.relayTerminal.id, nil)
	}
}

// Abandon abandons the expansion.
func (t *ExpansionRelayTerminal) Abandon(err *terminal.Error) {
	if t.abandoned.SetToIf(false, true) {
		// Init proper process.
		t.crane.AbandonTerminal(t.id, err)

		// End connected operation.
		t.op.End(err.Wrap("relay failed with"))
	}
}

// FmtID returns the expansion ID hierarchy.
func (op *ExpandOp) FmtID() string {
	return fmt.Sprintf("%s>%d r> %s#%d", op.opTerminal.FmtID(), op.ID(), op.relayTerminal.crane.ID, op.relayTerminal.id)
}

// FmtID returns the expansion ID hierarchy.
func (t *ExpansionRelayTerminal) FmtID() string {
	return fmt.Sprintf("%s#%d", t.crane.ID, t.id)
}
