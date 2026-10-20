package main

import (
	_ "embed"
	"strings"
)

// Situational guidance lives in reply_rules.go and is added per reply; this
// core stays short so every reply doesn't pay for rules that don't apply.
//
//go:embed config/persona-core.txt
var defaultPersonaRaw string

var defaultPersona = strings.TrimSpace(defaultPersonaRaw)
