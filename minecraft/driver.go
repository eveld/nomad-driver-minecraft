package minecraft

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/drivers/shared/eventer"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/drivers"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"github.com/hashicorp/nomad/plugins/shared/structs"

	rcon "github.com/seeruk/minecraft-rcon/rcon"
)

const (
	pluginName        = "minecraft"
	pluginVersion     = "v0.1.0"
	fingerprintPeriod = 30 * time.Second
	taskHandleVersion = 1
)

var (
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDriver,
		PluginApiVersions: []string{drivers.ApiVersion010},
		PluginVersion:     pluginVersion,
		Name:              pluginName,
	}

	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"address": hclspec.NewDefault(
			hclspec.NewAttr("address", "string", false),
			hclspec.NewLiteral(`"localhost:25525"`),
		),
		"password": hclspec.NewDefault(
			hclspec.NewAttr("password", "string", false),
			hclspec.NewLiteral(`"admin"`),
		),
	})

	taskConfigSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"entity": hclspec.NewDefault(
			hclspec.NewAttr("entity", "string", false),
			hclspec.NewLiteral(`"minecart"`),
		),
		"position": hclspec.NewDefault(
			hclspec.NewAttr("position", "string", false),
			hclspec.NewLiteral(`"0 0 0"`),
		),
	})

	// We don't really support anything, but need to set remote tasks to true.
	capabilities = &drivers.Capabilities{
		SendSignals: false,
		Exec:        false,
		FSIsolation: drivers.FSIsolationNone,
		RemoteTasks: true,
	}
)

// Config contains configuration information for the plugin
type Config struct {
	Address  string `codec:"address"`
	Password string `codec:"password"`
}

// TaskConfig contains configuration information for a task that runs with
// this plugin
type TaskConfig struct {
	ID       string `codec:"id"`
	Entity   string `codec:"entity"`
	Position string `codec:"position"`
}

// TaskState is the runtime state which is encoded in the handle returned to
// Nomad client.
// This information is needed to rebuild the task state and handler during
// recovery.
type TaskState struct {
	ReattachConfig *structs.ReattachConfig
	TaskConfig     *drivers.TaskConfig
	StartedAt      time.Time

	// TODO: add any extra important values that must be persisted in order
	// to restore a task.
	//
	// The plugin keeps track of its running tasks in a in-memory data
	// structure. If the plugin crashes, this data will be lost, so Nomad
	// will respawn a new instance of the plugin and try to restore its
	// in-memory representation of the running tasks using the RecoverTask()
	// method below.
	ID       string
	Entity   string
	Position string
}

// HelloDriverPlugin is an example driver plugin. When provisioned in a job,
// the taks will output a greet specified by the user.
type MinecraftDriverPlugin struct {
	// eventer is used to handle multiplexing of TaskEvents calls such that an
	// event can be broadcast to all callers
	eventer *eventer.Eventer

	// config is the plugin configuration set by the SetConfig RPC
	config *Config

	// nomadConfig is the client config from Nomad
	nomadConfig *base.ClientDriverConfig

	// tasks is the in memory datastore mapping taskIDs to driver handles
	tasks *taskStore

	// ctx is the context for the driver. It is passed to other subsystems to
	// coordinate shutdown
	ctx context.Context

	// signalShutdown is called when the driver is shutting down and cancels
	// the ctx passed to any subsystems
	signalShutdown context.CancelFunc

	// logger will log to the Nomad agent
	logger hclog.Logger

	client MinecraftClientInterface
}

// NewPlugin returns a new example driver plugin
func NewPlugin(logger hclog.Logger) drivers.DriverPlugin {
	ctx, cancel := context.WithCancel(context.Background())
	logger = logger.Named(pluginName)

	return &MinecraftDriverPlugin{
		eventer:        eventer.NewEventer(ctx, logger),
		config:         &Config{},
		tasks:          newTaskStore(),
		ctx:            ctx,
		signalShutdown: cancel,
		logger:         logger,
	}
}

// PluginInfo returns information describing the plugin.
func (d *MinecraftDriverPlugin) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

// ConfigSchema returns the plugin configuration schema.
func (d *MinecraftDriverPlugin) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

// SetConfig is called by the client to pass the configuration for the plugin.
func (d *MinecraftDriverPlugin) SetConfig(cfg *base.Config) error {
	var config Config
	if len(cfg.PluginConfig) != 0 {
		if err := base.MsgPackDecode(cfg.PluginConfig, &config); err != nil {
			return err
		}
	}

	// Save the configuration to the plugin
	d.config = &config

	// Parse and validated any configuration value if necessary.
	address := d.config.Address
	if address == "" {
		return fmt.Errorf("invalid address %s", d.config.Address)
	}
	// Verify that this is a valid rcon address....
	// something like <string>:<port> or <ip>:<port>

	password := d.config.Password
	if password == "" {
		return fmt.Errorf("invalid password %s", d.config.Password)
	}

	addressParts := strings.Split(address, ":")
	port, err := strconv.Atoi(addressParts[1])
	if err != nil {
		return fmt.Errorf("invalid port %s", addressParts[1])
	}

	// Save the Nomad agent configuration
	if cfg.AgentConfig != nil {
		d.nomadConfig = cfg.AgentConfig.Driver
	}

	// Initialize any extra requirements if necessary.
	client, err := rcon.NewClient(addressParts[0], port, password)
	if err != nil {
		return fmt.Errorf("could not create rcon client")
	}
	d.client = MinecraftClient{d.logger, client}

	return nil
}

// TaskConfigSchema returns the HCL schema for the configuration of a task.
func (d *MinecraftDriverPlugin) TaskConfigSchema() (*hclspec.Spec, error) {
	return taskConfigSpec, nil
}

// Capabilities returns the features supported by the driver.
func (d *MinecraftDriverPlugin) Capabilities() (*drivers.Capabilities, error) {
	return capabilities, nil
}

// Fingerprint returns a channel that will be used to send health information
// and other driver specific node attributes.
func (d *MinecraftDriverPlugin) Fingerprint(ctx context.Context) (<-chan *drivers.Fingerprint, error) {
	ch := make(chan *drivers.Fingerprint)
	go d.handleFingerprint(ctx, ch)
	return ch, nil
}

// handleFingerprint manages the channel and the flow of fingerprint data.
func (d *MinecraftDriverPlugin) handleFingerprint(ctx context.Context, ch chan<- *drivers.Fingerprint) {
	defer close(ch)

	// Nomad expects the initial fingerprint to be sent immediately
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.ctx.Done():
			return
		case <-ticker.C:
			// after the initial fingerprint we can set the proper fingerprint period
			ticker.Reset(fingerprintPeriod)
			ch <- d.buildFingerprint()
		}
	}
}

// buildFingerprint returns the driver's fingerprint data
func (d *MinecraftDriverPlugin) buildFingerprint() *drivers.Fingerprint {
	conn, err := net.Dial("tcp", d.config.Address)
	if err != nil {
		return &drivers.Fingerprint{
			Health:            drivers.HealthStateUndetected,
			HealthDescription: fmt.Sprintf("could not connect to minecraft: %s", d.config.Address),
		}
	}
	defer conn.Close()

	fp := &drivers.Fingerprint{
		Attributes:        map[string]*structs.Attribute{},
		Health:            drivers.HealthStateHealthy,
		HealthDescription: drivers.DriverHealthy,
	}

	return fp
}

// StartTask returns a task handle and a driver network if necessary.
func (d *MinecraftDriverPlugin) StartTask(cfg *drivers.TaskConfig) (*drivers.TaskHandle, *drivers.DriverNetwork, error) {
	if _, ok := d.tasks.Get(cfg.ID); ok {
		return nil, nil, fmt.Errorf("task with ID %q already started", cfg.ID)
	}

	var taskConfig TaskConfig
	if err := cfg.DecodeDriverConfig(&taskConfig); err != nil {
		return nil, nil, fmt.Errorf("failed to decode driver config: %v", err)
	}

	// Set the ID, now that we know it.
	taskConfig.ID = cfg.AllocID

	d.logger.Info("starting task", "driver_cfg", hclog.Fmt("%+v", taskConfig))
	handle := drivers.NewTaskHandle(taskHandleVersion)
	handle.Config = cfg

	id := cfg.AllocID
	entity := taskConfig.Entity
	position := taskConfig.Position

	err := d.client.SummonEntity(context.Background(), entity, position, id)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start minecraft task: %v", err)
	}

	driverState := TaskState{
		TaskConfig: cfg,
		StartedAt:  time.Now(),
		Entity:     entity,
		Position:   position,
		ID:         id,
	}

	d.logger.Info("minecraft task started", "entity", driverState.Entity, "started_at", driverState.StartedAt)
	h := newTaskHandle(d.logger, driverState, cfg, d.client)

	if err := handle.SetDriverState(&driverState); err != nil {
		d.logger.Error("failed to start task, error setting driver state", "error", err)
		return nil, nil, fmt.Errorf("failed to set driver state: %v", err)
	}

	d.tasks.Set(cfg.ID, h)

	go h.run()
	return handle, nil, nil
}

// RecoverTask recreates the in-memory state of a task from a TaskHandle.
func (d *MinecraftDriverPlugin) RecoverTask(handle *drivers.TaskHandle) error {
	if handle == nil {
		return errors.New("error: handle cannot be nil")
	}

	if _, ok := d.tasks.Get(handle.Config.ID); ok {
		return nil
	}

	var taskState TaskState
	if err := handle.GetDriverState(&taskState); err != nil {
		return fmt.Errorf("failed to decode task state from handle: %v", err)
	}

	var driverConfig TaskConfig
	if err := taskState.TaskConfig.DecodeDriverConfig(&driverConfig); err != nil {
		return fmt.Errorf("failed to decode driver config: %v", err)
	}

	// TODO: implement driver specific logic to recover a task.
	//
	// Recovering a task involves recreating and storing a taskHandle as if the
	// task was just started.
	//
	// In the example below we use the executor to re-attach to the process
	// that was created when the task first started.

	h := &taskHandle{
		taskConfig: taskState.TaskConfig,
		procState:  drivers.TaskStateRunning,
		startedAt:  taskState.StartedAt,
		exitResult: &drivers.ExitResult{},
		client:     d.client,
		entity:     taskState.Entity,
	}

	d.tasks.Set(taskState.TaskConfig.ID, h)

	go h.run()
	return nil
}

// WaitTask returns a channel used to notify Nomad when a task exits.
func (d *MinecraftDriverPlugin) WaitTask(ctx context.Context, taskID string) (<-chan *drivers.ExitResult, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	// Since minecraft tasks are somewhat ephemereal, what should we do here?
	ch := make(chan *drivers.ExitResult)
	go d.handleWait(ctx, handle, ch)
	return ch, nil
}

func (d *MinecraftDriverPlugin) handleWait(ctx context.Context, handle *taskHandle, ch chan *drivers.ExitResult) {
	defer close(ch)

	var result *drivers.ExitResult
	select {
	case <-ctx.Done():
		return
	case <-d.ctx.Done():
		return
	case <-handle.doneCh:
		result = &drivers.ExitResult{
			ExitCode: handle.exitResult.ExitCode,
			Signal:   handle.exitResult.Signal,
			Err:      nil,
		}
	}

	select {
	case <-ctx.Done():
		return
	case <-d.ctx.Done():
		return
	case ch <- result:
	}
}

// StopTask stops a running task with the given signal and within the timeout window.
func (d *MinecraftDriverPlugin) StopTask(taskID string, timeout time.Duration, signal string) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	d.logger.Info("minecraft task stopping", "entity", taskID)
	handle.cancel()

	return nil
}

// DestroyTask cleans up and removes a task that has terminated.
func (d *MinecraftDriverPlugin) DestroyTask(taskID string, force bool) error {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	if handle.IsRunning() && !force {
		return errors.New("cannot destroy running task")
	}

	// Is this any different from the stop task?
	// Should we call the same code?
	// TODO: implement driver specific logic to destroy a complete task.
	handle.cancel()
	d.tasks.Delete(taskID)
	return nil
}

// InspectTask returns detailed status information for the referenced taskID.
func (d *MinecraftDriverPlugin) InspectTask(taskID string) (*drivers.TaskStatus, error) {
	handle, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	return handle.TaskStatus(), nil
}

// TaskStats returns a channel which the driver should send stats to at the given interval.
func (d *MinecraftDriverPlugin) TaskStats(ctx context.Context, taskID string, interval time.Duration) (<-chan *drivers.TaskResourceUsage, error) {
	_, ok := d.tasks.Get(taskID)
	if !ok {
		return nil, drivers.ErrTaskNotFound
	}

	// Since the minecraft tasks don't really consume anything, should we do anything here?
	// TODO: implement driver specific logic to send task stats.

	return make(chan *drivers.TaskResourceUsage), nil
}

// TaskEvents returns a channel that the plugin can use to emit task related events.
func (d *MinecraftDriverPlugin) TaskEvents(ctx context.Context) (<-chan *drivers.TaskEvent, error) {
	return d.eventer.TaskEvents(ctx)
}

// SignalTask forwards a signal to a task.
// This is an optional capability.
func (d *MinecraftDriverPlugin) SignalTask(taskID string, signal string) error {
	_, ok := d.tasks.Get(taskID)
	if !ok {
		return drivers.ErrTaskNotFound
	}

	// Since our task is more fire and forget, we don't really use signals.
	return nil
}

// ExecTask returns the result of executing the given command inside a task.
// This is an optional capability.
func (d *MinecraftDriverPlugin) ExecTask(taskID string, cmd []string, timeout time.Duration) (*drivers.ExecTaskResult, error) {
	return nil, errors.New("this driver does not support exec")
}
