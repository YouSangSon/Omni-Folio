package main

import "omni-folio/services/core/internal/paperdomain"

const (
	paperFillPolicyVersion = paperdomain.FillPolicyVersion
	paperMaxQuantity       = paperdomain.MaxQuantity
)

func paperExecutionPolicy(policy strategyExecutionPolicy) paperdomain.ExecutionPolicy {
	return paperdomain.ExecutionPolicy{
		Fee: policy.Fee, Tax: policy.Tax, SlippageBPS: policy.SlippageBPS, MaxParticipation: policy.MaxParticipation,
	}
}
