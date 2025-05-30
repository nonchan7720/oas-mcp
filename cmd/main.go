package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/dave/jennifer/jen"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oapi-codegen/oapi-codegen/v2/pkg/codegen"
)

//go:generate go run main.go -path=../../api/openapi.yaml -output=../../pkg/client

func main() {
	/*
		- 引数に openapi のファイルパスをもらう
		- github.com/getkin/kin-openapi と github.com/oapi-codegen/oapi-codegen/v2 を使って openapi client を生成する
		- 生成した openapi client を利用して mcp server を作る
			- github.com/mark3labs/mcp-go を使ってMCP serverを作成
			- sse を使う
		- エンドポイント単位でmcp tools として提供する
			- mcp tools の構成は pkg/functions を使って作ってください。
		- //go:generate を使ってコマンドが実行される予定です。
		- mcp tool, mcp server のコード生成を行う際は github.com/dave/jennifer を使用してください。
	*/

	var openapiPath string
	var outputPath string
	var packageName string

	flag.StringVar(&openapiPath, "path", "", "OpenAPI specification file path")
	flag.StringVar(&outputPath, "output", "pkg/client", "Output directory for generated client")
	flag.StringVar(&packageName, "package", "client", "Package name for generated client")
	flag.Parse()

	if openapiPath == "" {
		log.Fatal("OpenAPI specification file path is required")
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true
	// OpenAPIファイルを読み込む
	o3, err := loader.LoadFromFile(openapiPath)
	if err != nil {
		log.Fatalf("Failed to read OpenAPI spec: %v", err)
	}

	setDescriptionTagOpenAPI(o3)

	// 出力ディレクトリを作成
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}
	err = generateClientByCodeGen(o3, outputPath, packageName)
	if err != nil {
		log.Fatalf("Failed to generate client: %v", err)
	}

	// MCP Tools を生成
	if err := generateMCPTools(o3, outputPath); err != nil {
		log.Fatalf("Failed to generate MCP tools: %v", err)
	}
	// hasSecuritySource := len(o3.Security) > 0 || len(o3.Components.SecuritySchemes) > 0
	// MCP Server ファイルを生成
	if err := generateMCPServer(o3, outputPath); err != nil {
		log.Fatalf("Failed to generate MCP server: %v", err)
	}

	log.Printf("Successfully generated OpenAPI client, MCP tools and server in %s", outputPath)
}

func setDescriptionTagOpenAPI(parsedSpec *openapi3.T) {
	// スキーマに再帰的にタグを設定する関数
	var setSchemaRecursive func(schema *openapi3.Schema, description string, name string)

	setSchemaRecursive = func(schema *openapi3.Schema, description string, name string) {
		if schema != nil && description != "" {
			// 現在のスキーマにタグを設定
			if len(schema.Extensions) == 0 {
				schema.Extensions = make(map[string]any)
			}
			content := map[string]any{
				"mcpdescription": strings.ReplaceAll(description, "`", ""),
			}
			schema.Extensions["x-oapi-codegen-extra-tags"] = content
		}

		// オブジェクトの場合、各プロパティを処理
		for name, prop := range schema.Properties {
			// propertyからスキーマを取得
			if prop.Value != nil {
				propDesc := prop.Value.Description
				setSchemaRecursive(prop.Value, propDesc, name)
			}
		}

		if schema.Items != nil {
			items := schema.Items
			if items.Value != nil {
				setSchemaRecursive(items.Value, "", "")
			}
		}

		// allOf, oneOf, anyOfを処理
		for _, s := range schema.AllOf {
			setSchemaRecursive(s.Value, s.Value.Description, "")
		}
		for _, s := range schema.OneOf {
			setSchemaRecursive(s.Value, s.Value.Description, "")
		}
		for _, s := range schema.AnyOf {
			setSchemaRecursive(s.Value, s.Value.Description, "")
		}
	}

	// パラメータを処理
	setParameter := func(parameters []*openapi3.ParameterRef) {
		for _, ref := range parameters {
			param := ref.Value
			if param.Description != "" && param.Schema != nil {
				setSchemaRecursive(param.Schema.Value, param.Description, param.Name)
				if param.Description != "" {
					// 現在のスキーマにタグを設定
					if len(param.Extensions) == 0 {
						param.Extensions = make(map[string]any)
					}
					content := map[string]any{
						"mcpdescription": strings.ReplaceAll(param.Description, "`", ""),
					}
					param.Extensions["x-oapi-codegen-extra-tags"] = content
				}
			}
		}
	}

	// リクエストボディを処理
	setRequestBody := func(ref *openapi3.RequestBodyRef) {
		if ref == nil || ref.Value == nil {
			return
		}
		body := ref.Value
		desc := body.Description
		for _, media := range body.Content {
			if media.Schema != nil && desc != "" {
				setSchemaRecursive(media.Schema.Value, desc, "")
				if desc != "" {
					// 現在のスキーマにタグを設定
					if len(media.Extensions) == 0 {
						media.Extensions = make(map[string]any)
					}
					content := map[string]any{
						"mcpdescription": strings.ReplaceAll(desc, "`", ""),
					}
					media.Extensions["x-oapi-codegen-extra-tags"] = content
				}
			}
		}
	}

	// パスと操作を処理
	for _, pathItem := range parsedSpec.Paths.Map() {
		for _, ope := range getOperations(pathItem) {
			setParameter(ope.Parameters)
			setRequestBody(ope.RequestBody)
		}
	}

	// コンポーネントを処理
	if parsedSpec.Components != nil {
		// パラメータを処理
		parameters := make([]*openapi3.ParameterRef, 0, len(parsedSpec.Components.Parameters))
		for _, parameter := range parsedSpec.Components.Parameters {
			parameters = append(parameters, parameter)
		}
		setParameter(parameters)

		// リクエストボディを処理
		for _, body := range parsedSpec.Components.RequestBodies {
			setRequestBody(body)
		}

		// スキーマを処理
		for name, schema := range parsedSpec.Components.Schemas {
			setSchemaRecursive(schema.Value, schema.Value.Description, name)
		}
	}
}

func generateClientByCodeGen(parsedSpec *openapi3.T, basePath, packageName string) error {
	outputPath := path.Join(basePath, "client")
	// 中間ステップを省略して、オリジナルのYAMLファイルを直接使用
	// 出力ディレクトリを絶対パスに変換
	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return err
	}
	switch files, err := os.ReadDir(absOutputPath); {
	case os.IsNotExist(err):
		if err := os.MkdirAll(absOutputPath, 0o750); err != nil {
			return err
		}
	default:
		if err := cleanDir(absOutputPath, files); err != nil {
			return fmt.Errorf("failed cleanDir: %w", err)
		}
	}

	code, err := codegen.Generate(parsedSpec, codegen.Configuration{
		PackageName: packageName,
		Generate: codegen.GenerateOptions{
			Client:       true,
			Models:       true,
			EmbeddedSpec: true,
		},
	})
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(absOutputPath, "client.gen.go"), []byte(code), 0o644)
}

// MCP Toolsを生成
func generateMCPTools(o3 *openapi3.T, outputPath string) error {
	// 各エンドポイントに対応するMCP Toolを生成
	toolsDir := filepath.Join(outputPath, "tools")

	// ディレクトリを作成
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return fmt.Errorf("failed to create tools directory: %w", err)
	}
	operationDefines, err := codegen.OperationDefinitions(o3, false)
	if err != nil {
		return err
	}
	for _, operation := range operationDefines {
		// MCPツールファイルを生成
		toolFilename := strings.ToLower(operation.OperationId) + "_tool.go"
		toolFilePath := filepath.Join(toolsDir, toolFilename)

		// Jenniferを使ってコードを生成
		if err := generateMCPToolWithJennifer(
			operation,
			toolFilePath,
		); err != nil {
			return fmt.Errorf("failed to generate tool for %s: %w", operation.OperationId, err)
		}
	}
	return nil
}

// Jenniferを使用してMCPツールコードを生成
func generateMCPToolWithJennifer(operation codegen.OperationDefinition, outputPath string) error {
	modName := getModuleName()
	outputDir := filepath.Dir(outputPath)
	basePath := strings.TrimSuffix(outputDir, "/tools")
	oasClient := modName + "/" + basePath + "/client"
	// function
	functions := "github.com/nonchan7720/oas-mcp/functions"

	toolDescription := operation.Spec.Description
	if toolDescription == "" {
		toolDescription = operation.Summary
	}

	inputStructName := operation.OperationId + "Input"
	f := jen.NewFile("tools")
	f.HeaderComment("Code generated by OpenAPI MCP generator. DO NOT EDIT.")
	f.ImportName("context", "context")
	f.ImportName("encoding/json", "json")
	f.ImportName(functions, "functions")
	f.ImportName(oasClient, "client")

	args := []jen.Code{jen.Id("ctx")}
	inputFields := []jen.Code{}
	for _, param := range operation.PathParams {
		tag := map[string]string{
			"json": param.GoName(),
		}
		desc := param.Schema.Description
		if param.Spec != nil && param.Spec.Description != "" {
			desc = param.Spec.Description
		}
		tag["mcpdescription"] = desc
		if param.Schema.GoType == param.GoName() {
			inputFields = append(inputFields,
				jen.Id(param.GoName()).Qual(oasClient, param.GoName()).Tag(tag),
			)
			args = append(args, jen.Id("input").Dot(param.GoName()))
		} else {
			inputFields = append(inputFields,
				jen.Id(param.GoName()).Op(param.Schema.GoType).Tag(tag),
			)
			args = append(args, jen.Id("input").Dot(param.GoName()))
		}
	}
	for _, body := range operation.Bodies {
		typeDef := body.TypeDef(operation.OperationId)
		tag := map[string]string{
			"json": body.Schema.RefType,
		}
		desc := ""
		if schema := body.Schema.OAPISchema; schema != nil {
			if extensions := schema.Extensions; extensions != nil {
				if tags, ok := extensions["x-oapi-codegen-extra-tags"].(map[string]any); ok {
					desc, _ = tags["mcpdescription"].(string)
				}
			}
		}
		tag["mcpdescription"] = desc
		inputFields = append(inputFields,
			jen.Id(typeDef.TypeName).Qual(oasClient, typeDef.TypeName).Tag(tag),
		)
		args = append(args, jen.Id("input").Dot(typeDef.TypeName))
	}
	if len(operation.Bodies) == 0 {
		for _, typeDef := range operation.TypeDefinitions {
			tag := map[string]string{
				"json": typeDef.TypeName,
			}
			desc := ""
			if schema := typeDef.Schema.OAPISchema; schema != nil {
				if extensions := schema.Extensions; extensions != nil {
					if tags, ok := extensions["x-oapi-codegen-extra-tags"].(map[string]any); ok {
						desc, _ = tags["mcpdescription"].(string)
					}
				}
			}
			tag["mcpdescription"] = desc
			inputFields = append(inputFields,
				jen.Id(typeDef.TypeName).Op("*").Qual(oasClient, typeDef.TypeName).Tag(tag),
			)
			args = append(args, jen.Id("input").Dot(typeDef.TypeName))
		}
	}
	if len(inputFields) == 0 {
		inputFields = append(inputFields, jen.Comment("// No parameters"))
	}
	f.Type().Id(inputStructName).Struct(inputFields...)
	for _, resp := range operation.Responses {
		fmt.Println(resp.StatusCode)
	}
	f.Comment(fmt.Sprintf("%s is a MCP tool for %s", operation.OperationId, toolDescription))
	f.Func().Id("New"+operation.OperationId+"Tool").Params(
		jen.Id("oasClient").Op("*").Qual(oasClient, "ClientWithResponses"),
	).Op("*").Qual(functions, "Tool").Types(jen.Id(inputStructName)).Block(
		jen.Return(
			jen.Qual(functions, "NewFunctionTool").Types(jen.Id(inputStructName)).Call(
				jen.Lit(operation.OperationId),
				jen.Lit(toolDescription),
				jen.Func().Params(
					jen.Id("ctx").Qual("context", "Context"),
					jen.Id("input").Id(inputStructName),
				).Params(jen.Any(), jen.Error()).BlockFunc(func(g *jen.Group) {
					g.List(jen.Id("resp"), jen.Id("err")).Op(":=").Id("oasClient").Dot(operation.OperationId + "WithResponse").Call(args...)
					g.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Nil(), jen.Id("err")),
					)
					for _, resp := range operation.Responses {
						if resp.StatusCode != "204" {
							body := fmt.Sprintf("JSON%s", resp.StatusCode)
							g.If(jen.Id("resp").Dot(body).Op("!=").Nil()).Block(
								jen.Return(jen.Id("resp").Dot(body), jen.Nil()),
							)
						}
					}
					g.Return(jen.Op("[]byte(`{\"status\":\"OK\"}`)"), jen.Nil())
				}),
			),
		),
	)

	return f.Save(outputPath)
}

// MCP Serverを生成
func generateMCPServer(o3 *openapi3.T, outputPath string) error {
	// サーバーディレクトリ
	serverDir := filepath.Join(outputPath, "server")

	// ディレクトリを作成
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return fmt.Errorf("failed to create server directory: %w", err)
	}

	// ツール名を収集
	var toolNames []string
	for _, pathItem := range o3.Paths.Map() {
		for _, ope := range getOperations(pathItem) {
			toolNames = append(toolNames, ope.OperationID)
		}
	}
	optionFilePath := filepath.Join(serverDir, "option.go")
	if err := generateMCPServerOptionsWithJennifer(optionFilePath); err != nil {
		return nil
	}

	// サーバーファイルパス
	serverFilePath := filepath.Join(serverDir, "server.go")
	// Jenniferを使ってサーバーコードを生成
	return generateMCPServerWithJennifer(toolNames, serverFilePath)
}

// Jenniferを使用してMCPサーバーコードを生成
func generateMCPServerWithJennifer(toolNames []string, outputPath string) error {
	// Prepare package paths
	outputDir := filepath.Dir(outputPath)
	basePath := strings.TrimSuffix(outputDir, "/server")
	modName := getModuleName()
	// Reference to client package
	oasClient := modName + "/" + basePath + "/client"
	// Reference to the tools package
	toolsPath := modName + "/" + basePath + "/tools"
	// Reference to the mcp server package
	mcpServer := "github.com/mark3labs/mcp-go/server"

	// file creation
	f := jen.NewFile("server")

	// file comment
	f.HeaderComment("Code generated by OpenAPI MCP generator. DO NOT EDIT.")

	// インポート
	f.ImportName("context", "context")
	f.ImportName("log/slog", "slog")
	f.ImportName("net/http", "http")
	f.ImportName("os", "os")
	f.ImportName("os/signal", "signal")
	f.ImportName("syscall", "syscall")
	f.ImportName("github.com/mark3labs/mcp-go/server", "server")
	// 生成されたOpenAPIクライアントとツールのパスを指定
	f.ImportName(oasClient, "client")
	f.ImportName(toolsPath, "tools")

	funcBody := []jen.Code{
		// option
		jen.Id("opt").Op(":=").Op("&").Id("option").Block(),
		jen.For().Id("_").Op(",").Id("o").Op(":=").Range().Id("opts").Block(
			jen.Id("o").CallFunc(func(g *jen.Group) {
				g.Id("opt")
			}),
		),

		// client initialization
		jen.Comment("client initialization"),
		jen.List(jen.Id("client"), jen.Id("err")).Op(":=").Qual(oasClient, "NewClientWithResponses").CallFunc(func(g *jen.Group) {
			g.Id("apiServerURL")
			g.Id("opt").Dot("clientOptions").Op("...")
		}),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Nil(), jen.Id("err")),
		),
		jen.Line(),
		// MCP server initialization
		jen.Comment("MCP server initialization"),
		jen.Id("mcpServer").Op(":=").Qual(mcpServer, "NewMCPServer").Call(
			jen.Id("name"),
			jen.Id("version"),
			jen.Id("opt").Dot("mcpServerOptions").Op("..."),
		),
	}

	funcBody = append(funcBody,
		jen.Comment("Register all tools"),
		jen.Id("mcpServer").Dot("AddTools").Call(jen.Line().ListFunc(func(g *jen.Group) {
			for idx, toolName := range toolNames {
				if idx > 0 {
					g.Line().Qual(toolsPath, "New"+toolName+"Tool").Call(jen.Id("client")).Dot("ServerTool").Call()
				} else {
					g.Qual(toolsPath, "New"+toolName+"Tool").Call(jen.Id("client")).Dot("ServerTool").Call()
				}
			}
			g.Line()
		})),
		jen.Id("server").Op(":=").Qual(mcpServer, "NewStreamableHTTPServer").Call(
			jen.Id("mcpServer"),
			jen.Id("opt").Dot("streamableOptions").Op("..."),
		),
		jen.Line(),
		jen.Return(jen.Id("server"), jen.Nil()),
	)

	// Added NewServer function
	f.Comment("NewServer MCP server with all generated tools")
	f.Func().Id("NewServer").ParamsFunc(func(g *jen.Group) {
		g.Id("ctx").Qual("context", "Context")
		g.Id("name")
		g.Id("version")
		g.Id("apiServerURL").String()
		g.Id("opts").Op("...").Id("Option")
	}).Call(
		jen.Op("*").Qual(mcpServer, "StreamableHTTPServer"),
		jen.Error(),
	).Block(funcBody...)

	// Save to File
	return f.Save(outputPath)
}

func generateMCPServerOptionsWithJennifer(outputPath string) error {
	// Prepare package paths
	outputDir := filepath.Dir(outputPath)
	basePath := strings.TrimSuffix(outputDir, "/server")
	modName := getModuleName()
	// Reference to client package
	oasClient := modName + "/" + basePath + "/client"
	// Reference to the mcp server package
	mcpServer := "github.com/mark3labs/mcp-go/server"

	// file creation
	f := jen.NewFile("server")

	// file comment
	f.HeaderComment("Code generated by OpenAPI MCP generator. DO NOT EDIT.")

	// インポート
	f.ImportName(mcpServer, "server")

	// option 構造体
	f.Type().Id("option").Struct(
		jen.Id("mcpServerOptions").Op("[]").Qual(mcpServer, "ServerOption"),
		jen.Id("streamableOptions").Op("[]").Qual(mcpServer, "StreamableHTTPOption"),
		jen.Id("clientOptions").Op("[]").Qual(oasClient, "ClientOption"),
	)

	// Option 型
	f.Type().Id("Option").Func().Params(
		jen.Id("opt").Op("*").Id("option"),
	)

	// WithMCPServerOptions 関数
	f.Comment("WithMCPServerOptions adds MCP server options")
	f.Func().Id("WithMCPServerOptions").Params(
		jen.Id("mcpServerOptions").Op("...").Qual(mcpServer, "ServerOption"),
	).Id("Option").Block(
		jen.Return(
			jen.Func().Params(
				jen.Id("opt").Op("*").Id("option"),
			).Block(
				jen.Id("opt").Dot("mcpServerOptions").Op("=").Id("append").Call(
					jen.Id("opt").Dot("mcpServerOptions"),
					jen.Id("mcpServerOptions").Op("..."),
				),
			),
		),
	)

	// WithStreamableOptions 関数
	f.Comment("WithStreamableOptions adds streamable HTTP options")
	f.Func().Id("WithStreamableOptions").Params(
		jen.Id("streamableOptions").Op("...").Qual(mcpServer, "StreamableHTTPOption"),
	).Id("Option").Block(
		jen.Return(
			jen.Func().Params(
				jen.Id("opt").Op("*").Id("option"),
			).Block(
				jen.Id("opt").Dot("streamableOptions").Op("=").Id("append").Call(
					jen.Id("opt").Dot("streamableOptions"),
					jen.Id("streamableOptions").Op("..."),
				),
			),
		),
	)

	f.Comment("WithClientOptions openapi client options")
	f.Func().Id("WithClientOptions").Params(
		jen.Id("clientOptions").Op("...").Qual(oasClient, "ClientOption"),
	).Id("Option").Block(
		jen.Return(
			jen.Func().Params(
				jen.Id("opt").Op("*").Id("option"),
			).Block(
				jen.Id("opt").Dot("clientOptions").Op("=").Id("append").Call(
					jen.Id("opt").Dot("clientOptions"),
					jen.Id("clientOptions").Op("..."),
				),
			),
		),
	)

	// ファイルに保存
	return f.Save(outputPath)
}

// Helper function to retrieve an operation from a PathItem
func getOperations(pathItem *openapi3.PathItem) map[string]*openapi3.Operation {
	operations := make(map[string]*openapi3.Operation)

	if pathItem.Get != nil {
		operations["get"] = pathItem.Get
	}
	if pathItem.Post != nil {
		operations["post"] = pathItem.Post
	}
	if pathItem.Put != nil {
		operations["put"] = pathItem.Put
	}
	if pathItem.Delete != nil {
		operations["delete"] = pathItem.Delete
	}
	if pathItem.Patch != nil {
		operations["patch"] = pathItem.Patch
	}
	if pathItem.Options != nil {
		operations["options"] = pathItem.Options
	}

	return operations
}

// getModuleName はgo.modファイルからモジュール名を取得する
func getModuleName() string {
	// カレントディレクトリから親ディレクトリに向かってgo.modを探す
	dir, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get current directory: %v", err)
		return ""
	}

	for {
		gomod := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			// go.modファイルを見つけた
			content, err := os.ReadFile(gomod)
			if err != nil {
				log.Printf("Failed to read go.mod: %v", err)
				return ""
			}

			// モジュール名を抽出
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return strings.TrimSpace(line[7:]) // "module "の後の文字列
				}
			}
			return ""
		}

		// 親ディレクトリへ
		parent := filepath.Dir(dir)
		if parent == dir {
			// これ以上上がれない
			break
		}
		dir = parent
	}

	return ""
}

func cleanDir(targetDir string, files []os.DirEntry) (rerr error) {
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if !strings.HasSuffix(name, "_gen.go") && !strings.HasSuffix(name, "_gen_test.go") {
			continue
		}
		if !strings.HasPrefix(name, "openapi") && !strings.HasPrefix(name, "oas") {
			continue
		}
		// Do not return error if file does not exist.
		if err := os.Remove(filepath.Join(targetDir, name)); err != nil && !os.IsNotExist(err) {
			// Do not stop on first error, try to remove all files.
			rerr = errors.Join(rerr, err)
		}
	}
	return rerr
}

// ユーティリティ: スネーク/キャメル→パスカルケース
func toGoFieldName(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
