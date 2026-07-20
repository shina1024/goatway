package targetgroup

import (
	"fmt"

	"goatway/internal/config"
)

// Registry resolves target groups by their configured ID.
type Registry struct {
	groups map[config.TargetGroupID]*TargetGroup
}

// TargetGroupNotFoundError identifies an unknown target-group ID.
type TargetGroupNotFoundError struct {
	ID config.TargetGroupID
}

func (err *TargetGroupNotFoundError) Error() string {
	return fmt.Sprintf("target group %q was not found", err.ID)
}

// NewRegistry creates all groups before resolving cross-group retry pointers.
func NewRegistry(configs map[config.TargetGroupID]config.TargetGroupConfig) (*Registry, error) {
	groups := make(map[config.TargetGroupID]*TargetGroup, len(configs))
	for id, groupConfig := range configs {
		group, err := newTargetGroup(id, groupConfig)
		if err != nil {
			return nil, fmt.Errorf("create target group %q: %w", id, err)
		}
		groups[id] = group
	}

	for _, group := range groups {
		if group.retryToTargetGroupID == "" {
			continue
		}
		retryGroup, exists := groups[group.retryToTargetGroupID]
		if !exists {
			return nil, &TargetGroupNotFoundError{ID: group.retryToTargetGroupID}
		}
		group.retryToTargetGroup = retryGroup
	}

	return &Registry{groups: groups}, nil
}

// Lookup returns a configured target group or a typed not-found error.
func (registry *Registry) Lookup(id config.TargetGroupID) (*TargetGroup, error) {
	group, exists := registry.groups[id]
	if !exists {
		return nil, &TargetGroupNotFoundError{ID: id}
	}
	return group, nil
}
