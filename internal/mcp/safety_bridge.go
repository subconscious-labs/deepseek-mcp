package mcp

import "github.com/subconscious-labs/deepseek-mcp/internal/safety"

func safetyValidateURL(raw string, allowPrivate, resolveDNS bool) (string, error) {
	return safety.ValidateURL(raw, allowPrivate, resolveDNS)
}
