// Package domain contains platform-independent identities and message contracts.
package domain

import "errors"

const LocalWorkspaceID = "local-default"

var ErrScopeDenied = errors.New("resource is outside the authenticated scope")

type ConnectionScope struct {
	WorkspaceID  string `json:"workspace_id"`
	ConnectionID string `json:"connection_id"`
}

func (s ConnectionScope) Validate() error {
	if s.WorkspaceID == "" || s.ConnectionID == "" {
		return ErrScopeDenied
	}
	return nil
}

// AssertWorkspace must use the server-authenticated workspace, never a request field.
func (s ConnectionScope) AssertWorkspace(authenticatedWorkspace string) error {
	if s.Validate() != nil || authenticatedWorkspace == "" || s.WorkspaceID != authenticatedWorkspace {
		return ErrScopeDenied
	}
	return nil
}

type ConversationScope struct {
	ConnectionScope
	ContactID      string `json:"contact_id"`
	ConversationID string `json:"conversation_id"`
}

func (s ConversationScope) Validate() error {
	if s.ConnectionScope.Validate() != nil || s.ContactID == "" || s.ConversationID == "" {
		return ErrScopeDenied
	}
	return nil
}

func (s ConversationScope) AssertWorkspace(authenticatedWorkspace string) error {
	if s.Validate() != nil {
		return ErrScopeDenied
	}
	return s.ConnectionScope.AssertWorkspace(authenticatedWorkspace)
}
