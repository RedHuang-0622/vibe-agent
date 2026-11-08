package app

import (
	"sort"

	"vibe-agent/internal/accounts/domain"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

func ValidateCreate(cmd accountv1.CreateAccount) accountv1.ValidationReport {
	account := domain.Account{
		ID: cmd.AccountID, Revision: 1, Enabled: cmd.Enabled,
		DisplayName: cmd.DisplayName, Priority: cmd.Priority,
		DefaultFor: make(map[domain.Capability]bool), Services: mapBindings(cmd.Services),
	}
	for _, capability := range cmd.DefaultFor {
		account.DefaultFor[domain.Capability(capability)] = true
	}
	err := account.Validate()
	if err == nil {
		return accountv1.ValidationReport{Valid: true}
	}
	var issues []accountv1.ValidationIssue
	for _, item := range flattenErrors(err) {
		issues = append(issues, accountv1.ValidationIssue{Code: "invalid_account", Message: item.Error()})
	}
	sort.Slice(issues, func(i, j int) bool { return issues[i].Message < issues[j].Message })
	return accountv1.ValidationReport{Valid: false, Issues: issues}
}

func flattenErrors(err error) []error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var result []error
		for _, child := range joined.Unwrap() {
			result = append(result, flattenErrors(child)...)
		}
		return result
	}
	if err == nil {
		return nil
	}
	return []error{err}
}
