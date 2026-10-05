package model

type Diagnostic struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	File     string `json:"file"`
	Field    string `json:"field,omitempty"`
	Message  string `json:"message"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
}

func Error(file, field, message string) Diagnostic {
	return Diagnostic{Severity: "error", Code: "invalid_config", File: file, Field: field, Message: message}
}
