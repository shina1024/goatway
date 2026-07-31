package config

func (config Config) Validate() error {
	if err := config.validateTargetGroups(); err != nil {
		return err
	}
	if err := config.validateRoutes(); err != nil {
		return err
	}
	if err := config.validateCrossGroupRetries(); err != nil {
		return err
	}
	if err := config.validateIPRangeGroups(); err != nil {
		return err
	}
	if err := config.validateTokens(); err != nil {
		return err
	}
	if err := config.validateGateway(); err != nil {
		return err
	}
	if config.Deployment.PrimaryWeight < 0 || config.Deployment.CanaryWeight < 0 {
		return invalid("deployment.yml", "weights", "negative weight", "")
	}
	if config.Deployment.PrimaryPods < 0 || config.Deployment.CanaryPods < 0 {
		return invalid("deployment.yml", "pods", "negative pod count", "")
	}
	totalWeight := config.Deployment.PrimaryWeight + config.Deployment.CanaryWeight
	if totalWeight != 0 && totalWeight != 100 {
		return invalid("deployment.yml", "weights", "traffic weights must total 100 or 0", "")
	}
	for client, maximum := range config.MaxConcurrentRequests {
		if maximum < 0 {
			return invalid("max_concurrent_requests.yml", string(client), "negative max concurrent requests", "")
		}
	}
	return nil
}

func validateWeights(file, field string, weights []Weight) error {
	hasZero := false
	hasPositive := false
	for _, weight := range weights {
		if weight < 0 {
			return invalid(file, field, "negative weight", "")
		}
		hasZero = hasZero || weight == 0
		hasPositive = hasPositive || weight > 0
	}
	if hasZero && hasPositive {
		return invalid(file, field, "mixed weighted and nonweighted", "")
	}
	return nil
}

func targetWeights(targets []TargetConfig) []Weight {
	weights := make([]Weight, len(targets))
	for index, target := range targets {
		weights[index] = target.Weight
	}
	return weights
}

func destinationWeights(destinations []DestinationConfig) []Weight {
	weights := make([]Weight, len(destinations))
	for index, destination := range destinations {
		weights[index] = destination.Weight
	}
	return weights
}
