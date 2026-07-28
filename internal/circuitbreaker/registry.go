package circuitbreaker

// Registry stores one breaker for each configured target group.
type Registry struct {
	breakers map[string]*Breaker
}

// NewRegistry creates breakers for the configured target group IDs.
func NewRegistry(groupIDs []string, config Config) *Registry {
	breakers := make(map[string]*Breaker, len(groupIDs))
	for _, groupID := range groupIDs {
		breakers[groupID] = New(config)
	}
	return &Registry{breakers: breakers}
}

// Breaker returns the configured breaker for a target group.
func (registry *Registry) Breaker(groupID string) *Breaker {
	if registry == nil {
		return nil
	}
	return registry.breakers[groupID]
}
