package api

import "github.com/qilinbro/msg-manager/internal/connector"

const Version = "0.1.0"

type AutomationStatus struct {
	Paused          bool `json:"paused"`
	AutoSendEnabled bool `json:"auto_send_enabled"`
}

type StorageStatus struct {
	Configured bool   `json:"configured"`
	Driver     string `json:"driver"`
}

type SystemResponse struct {
	Version     string                 `json:"version"`
	Stage       string                 `json:"stage"`
	WorkspaceID string                 `json:"workspace_id"`
	Automation  AutomationStatus       `json:"automation"`
	Storage     StorageStatus          `json:"storage"`
	Connectors  []connector.Descriptor `json:"connectors"`
}

type ListResponse[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	TraceID string `json:"trace_id"`
}

type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}
