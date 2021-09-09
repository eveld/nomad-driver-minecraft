package minecraft

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-hclog"
	rcon "github.com/seeruk/minecraft-rcon/rcon"
)

const (
	NoEntityFoundErr = "No entity was found"
)

type MinecraftClient struct {
	logger hclog.Logger
	client *rcon.Client
}

type EntityStatus struct {
	ID       string
	Entity   string
	Position string
}

type MinecraftClientInterface interface {
	// DescribeServer(ctx context.Context) error
	// DescribeTaskStatus(ctx context.Context, entityName string) (string, error)
	// RunTask(ctx context.Context, cfg TaskConfig) (string, error)
	// StopTask(ctx context.Context, entity string, id string) error

	CreateBlock() error
	DestroyBlock() error

	SummonEntity(ctx context.Context, entity string, position string, id string) error
	RemoveEntity(ctx context.Context, entity string, id string) error
	DescribeEntity(ctx context.Context, entity string, id string) (*EntityStatus, error)
}

func (c MinecraftClient) CreateBlock() error {
	return nil
}

func (c MinecraftClient) DestroyBlock() error {
	return nil
}

func (c MinecraftClient) SummonEntity(ctx context.Context, entity string, position string, id string) error {
	// Create the entity.
	command := fmt.Sprintf("summon minecraft:%s %s {CustomName:'{\"text\":\"%s\"}'}", entity, position, id)
	_, err := c.client.SendCommand(command)
	if err != nil {
		c.logger.Error("Could not send command to minecraft", "command", command, "error", err)
		return err
	}

	// Play a sound.
	command = fmt.Sprintf("playsound minecraft:entity.item.pickup block @a %s", position)
	_, err = c.client.SendCommand(command)
	if err != nil {
		c.logger.Error("Could not send command to minecraft", "command", command, "error", err)
		return err
	}

	return nil
}

func (c MinecraftClient) RemoveEntity(ctx context.Context, entity string, id string) error {
	// Remove the entity.
	command := fmt.Sprintf("kill @e[type=minecraft:%s,nbt={CustomName:'{\"text\":\"%s\"}'}]", entity, id)
	_, err := c.client.SendCommand(command)
	if err != nil {
		c.logger.Error("Could not send command to minecraft", "command", command, "error", err)
		return err
	}

	// Remove the entity from inventories.
	command = fmt.Sprintf("clear @a minecraft:%s{display:{Name:'{\"text\":\"%s\"}'}}", entity, id)
	_, err = c.client.SendCommand(command)
	if err != nil {
		c.logger.Error("Could not send command to minecraft", "command", command, "error", err)
		return err
	}

	return nil
}

func (c MinecraftClient) DescribeEntity(ctx context.Context, entity string, id string) (*EntityStatus, error) {
	command := fmt.Sprintf("data get entity @e[limit=1,type=minecraft:%s,nbt={CustomName:'{\"text\":\"%s\"}'}]", entity, id)
	response, err := c.client.SendCommand(command)
	if err != nil {
		c.logger.Error("Could not send command to minecraft", "command", command, "error", err)
		return nil, err
	}

	if response == NoEntityFoundErr {
		c.logger.Info("Could not find the entity", "entity", entity, "id", id)
		return nil, fmt.Errorf("entity not found")
	}
	return &EntityStatus{}, nil
}
