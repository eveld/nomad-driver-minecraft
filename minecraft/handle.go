package minecraft

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins/drivers"
)

// taskHandle should store all relevant runtime information
// such as process ID if this is a local task or other meta
// data if this driver deals with external APIs
type taskHandle struct {
	// stateLock syncs access to all fields below
	stateLock sync.RWMutex

	logger      hclog.Logger
	taskConfig  *drivers.TaskConfig
	procState   drivers.TaskState
	startedAt   time.Time
	completedAt time.Time
	exitResult  *drivers.ExitResult

	doneCh chan struct{}
	ctx    context.Context
	cancel context.CancelFunc

	client   MinecraftClientInterface
	id       string
	entity   string
	position string
}

func newTaskHandle(logger hclog.Logger, ts TaskState, taskConfig *drivers.TaskConfig, client MinecraftClientInterface) *taskHandle {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named("handle").With("entity", ts.Entity).With("id", ts.ID)

	h := &taskHandle{
		taskConfig: taskConfig,
		procState:  drivers.TaskStateRunning,
		startedAt:  ts.StartedAt,
		exitResult: &drivers.ExitResult{},
		logger:     logger,

		doneCh: make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,

		client:   client,
		id:       ts.ID,
		entity:   ts.Entity,
		position: ts.Position,
	}

	return h
}

func (h *taskHandle) TaskStatus() *drivers.TaskStatus {
	h.stateLock.RLock()
	defer h.stateLock.RUnlock()

	return &drivers.TaskStatus{
		ID:          h.taskConfig.ID,
		Name:        h.taskConfig.Name,
		State:       h.procState,
		StartedAt:   h.startedAt,
		CompletedAt: h.completedAt,
		ExitResult:  h.exitResult,
		DriverAttributes: map[string]string{
			"id":       h.id,
			"entity":   h.entity,
			"position": h.position,
		},
	}
}

func (h *taskHandle) IsRunning() bool {
	h.stateLock.RLock()
	defer h.stateLock.RUnlock()
	return h.procState == drivers.TaskStateRunning
}

func (h *taskHandle) run() {
	defer close(h.doneCh)
	h.stateLock.Lock()
	if h.exitResult == nil {
		h.exitResult = &drivers.ExitResult{}
	}
	h.stateLock.Unlock()

	err := h.getStatus()
	if err != nil {
		h.logger.Debug("Crashed out of the loop, removing entity?")
		h.client.RemoveEntity(h.ctx, h.entity, h.id)
		h.procState = drivers.TaskStateExited
		h.completedAt = time.Now()
		h.exitResult.ExitCode = 1
		h.exitResult.Err = fmt.Errorf("failed to remove entity: %v", err)
		h.cancel()
		return
	}

	h.stateLock.Lock()
	defer h.stateLock.Unlock()

	h.logger.Error("Removing entity")
	if err := h.client.RemoveEntity(h.ctx, h.entity, h.id); err != nil {
		h.logger.Error("Could not remove entity", "error", err)
		h.completedAt = time.Now()
		h.exitResult.ExitCode = 1
		h.exitResult.Err = fmt.Errorf("failed to remove entity: %v", err)
		h.cancel()
		return
	}

	h.procState = drivers.TaskStateExited
	h.exitResult.ExitCode = 0
	h.exitResult.Signal = 0
	h.completedAt = time.Now()
}

func (h *taskHandle) getStatus() error {
	for {
		select {
		case <-time.After(5 * time.Second):
			_, err := h.client.DescribeEntity(h.ctx, h.entity, h.id)
			if err != nil {
				h.logger.Error("Describe entity failed", "error", err)
				h.stateLock.Lock()
				h.completedAt = time.Now()
				h.exitResult.ExitCode = 1
				h.exitResult.Err = fmt.Errorf("could not describe entity: %v", err)
				h.procState = drivers.TaskStateExited
				h.stateLock.Unlock()
				h.cancel()
				return fmt.Errorf("could not describe entity: %v", err)
			}

		case <-h.ctx.Done():
			return nil
		}
	}
}
