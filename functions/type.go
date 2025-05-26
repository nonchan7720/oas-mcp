package functions

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
)

type MCPTool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, buf []byte) (any, error)
}

type Function func(ctx context.Context, params any) (any, error)

type Tool[T any] struct {
	name        string
	description string
	function    Execute[T]
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
	_           MCPTool = (*Tool[any])(nil)
	ErrRequired         = errors.New("Required.")
)
