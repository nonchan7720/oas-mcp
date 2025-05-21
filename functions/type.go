package functions

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
)

type MCPTool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, params map[string]any) (any, error)
}

type Function func(ctx context.Context, params any) (any, error)

type Tool struct {
	name        string
	description string
	function    any
	schema      *Schema
}

type Schema struct {
	Type       string
	Properties map[string]any
	Required   []string
}

func (s *Schema) MCPTool() mcp.ToolInputSchema {
	return mcp.ToolInputSchema{
		Type:       s.Type,
		Properties: s.Properties,
		Required:   s.Required,
	}
}

var (
	_           MCPTool = (*Tool)(nil)
	ErrRequired         = errors.New("Required.")
)
