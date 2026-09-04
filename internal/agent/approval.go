package agent

import (
	"context"
	"errors"

	"github.com/kkestell/ox/internal/acp"
)

const (
	permissionAllowOnceID   = "allow_once"
	permissionAllowAlwaysID = "allow_always"
	permissionRejectOnceID  = "reject_once"
)

type approvalDecision string

const (
	decisionAllowOnce   approvalDecision = "allow_once"
	decisionAllowAlways approvalDecision = "allow_always"
	decisionRefused     approvalDecision = "refused"
	decisionCancelled   approvalDecision = "cancelled"
)

func permissionOptions(rule string, ruleScoped bool) []acp.PermissionOption {
	options := []acp.PermissionOption{{
		OptionID: permissionAllowOnceID,
		Name:     "Allow once",
		Kind:     acp.PermissionOptionAllowOnce,
	}}
	if !ruleScoped || rule != "" {
		name := "Allow for this session"
		if ruleScoped {
			name = `Allow "` + rule + `" for this session`
		}
		options = append(options, acp.PermissionOption{
			OptionID: permissionAllowAlwaysID,
			Name:     name,
			Kind:     acp.PermissionOptionAllowAlways,
		})
	}
	return append(options, acp.PermissionOption{
		OptionID: permissionRejectOnceID,
		Name:     "Reject",
		Kind:     acp.PermissionOptionRejectOnce,
	})
}

func decideApproval(
	response acp.RequestPermissionResponse,
	err error,
) approvalDecision {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return decisionCancelled
		}
		return decisionRefused
	}
	if response.Outcome.Outcome == "cancelled" {
		return decisionCancelled
	}
	if response.Outcome.Outcome != "selected" {
		return decisionRefused
	}
	switch response.Outcome.OptionID {
	case permissionAllowOnceID:
		return decisionAllowOnce
	case permissionAllowAlwaysID:
		return decisionAllowAlways
	default:
		return decisionRefused
	}
}
