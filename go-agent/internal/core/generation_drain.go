package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sakullla/nginx-reverse-emby/go-agent/internal/generation"
)

// GenerationDrain connects a published GenerationView to protocol-neutral
// session ownership. Protocol adapters supply entity changes and sessions.
type GenerationDrain struct {
	controller *generation.DrainController
}

func NewGenerationDrain(controller *generation.DrainController) *GenerationDrain {
	if controller == nil {
		controller = generation.NewDrainController(nil)
	}
	return &GenerationDrain{controller: controller}
}

func (d *GenerationDrain) Controller() *generation.DrainController {
	if d == nil {
		return nil
	}
	return d.controller
}

func (d *GenerationDrain) Activate(
	ctx context.Context,
	cutover GenerationCutover,
	changes []generation.EntityChange,
	timeout time.Duration,
) error {
	if d == nil || d.controller == nil {
		return errors.New("generation drain is not configured")
	}
	if cutover.Active == nil {
		return errors.New("generation cutover has no active view")
	}

	current := d.controller.Snapshot().ActiveGenerationID
	if cutover.Previous == nil {
		if current != "" {
			return fmt.Errorf("generation cutover missing previous view for %s", current)
		}
	} else if current != cutover.Previous.ID() {
		return fmt.Errorf("generation cutover previous view %s does not match drain owner %s", cutover.Previous.ID(), current)
	}

	return d.controller.Activate(ctx, generation.Generation{
		ID:       cutover.Active.ID(),
		Revision: cutover.Active.Revision(),
		Resource: cutover.Active,
	}, changes, timeout)
}
