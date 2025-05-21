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
	"github.com/go-faster/yaml"
	"github.com/ogen-go/ogen"
	"github.com/ogen-go/ogen/gen"
	"github.com/ogen-go/ogen/gen/genfs"
	"github.com/ogen-go/ogen/gen/ir"
	"github.com/ogen-go/ogen/jsonschema"
)

//go:generate go run main.go -path=../../api/openapi.yaml -output=../../pkg/client

func main() {
	/*
		- 引数に openapi のファイルパスをもらう
		- github.com/ogen-go/ogen を使って openapi client を生成する
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

	// OpenAPIファイルを読み込む
	spec, err := os.ReadFile(openapiPath)
	if err != nil {
		log.Fatalf("Failed to read OpenAPI spec: %v", err)
	}

	// OpenAPIパーサーでパース
	parsedSpec, err := ogen.Parse(spec)
	if err != nil {
		log.Fatalf("Failed to parse OpenAPI spec: %v", err)
	}
	setDescriptionTag(parsedSpec)
	// 出力ディレクトリを作成
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	// ogen を使ってクライアントコードを生成
	g, err := generateClient(parsedSpec, outputPath, packageName)
	if err != nil {
		log.Fatalf("Failed to generate client: %v", err)
	}

	// MCP Tools を生成
	if err := generateMCPTools(g, outputPath); err != nil {
		log.Fatalf("Failed to generate MCP tools: %v", err)
	}

	// MCP Server ファイルを生成
	if err := generateMCPServer(g, outputPath); err != nil {
		log.Fatalf("Failed to generate MCP server: %v", err)
	}

	log.Printf("Successfully generated OpenAPI client, MCP tools and server in %s", outputPath)
}

func setDescriptionTag(parsedSpec *ogen.Spec) {
	// スキーマに再帰的にタグを設定する関数
	var setSchemaRecursive func(schema *ogen.Schema, description string)

	setSchemaRecursive = func(schema *ogen.Schema, description string) {
		if schema != nil && description != "" {
			// 現在のスキーマにタグを設定
			if len(schema.Common.Extensions) == 0 {
				schema.Common.Extensions = make(jsonschema.Extensions)
			}
			schema.Common.Extensions["x-oapi-codegen-extra-tags"] = yaml.Node{
				Kind: yaml.MappingNode,
				Tag:  "!!map",
				Content: []*yaml.Node{
					{
						Kind:  yaml.ScalarNode,
						Tag:   "!!str",
						Value: "mcpdescription",
					},
					{
						Kind:  yaml.ScalarNode,
						Tag:   "!!str",
						Value: description,
					},
				},
			}
		}

		// オブジェクトの場合、各プロパティを処理
		for _, prop := range schema.Properties {
			// propertyからスキーマを取得
			if prop.Schema != nil {
				propDesc := prop.Schema.Description
				if propDesc == "" {
					propDesc = prop.Schema.Summary
				}
				if propDesc == "" {
					propDesc = prop.Name
				}
				setSchemaRecursive(prop.Schema, propDesc)
			}
		}

		if schema.Items != nil {
			items := schema.Items
			if items.Item != nil {
				setSchemaRecursive(items.Item, "")
			}
			for _, item := range items.Items {
				setSchemaRecursive(item, "")
			}
		}

		// allOf, oneOf, anyOfを処理
		for _, s := range schema.AllOf {
			setSchemaRecursive(s, s.Description)
		}
		for _, s := range schema.OneOf {
			setSchemaRecursive(s, s.Description)
		}
		for _, s := range schema.AnyOf {
			setSchemaRecursive(s, s.Description)
		}
	}

	// パラメータを処理
	setParameter := func(parameters []*ogen.Parameter) {
		for _, param := range parameters {
			if param.Description != "" && param.Schema != nil {
				setSchemaRecursive(param.Schema, param.Description)
			}
		}
	}

	// リクエストボディを処理
	setRequestBody := func(body *ogen.RequestBody) {
		if body == nil {
			return
		}
		for _, media := range body.Content {
			if media.Schema != nil && (media.Schema.Description != "" || media.Schema.Summary != "") {
				desc := media.Schema.Description
				if desc == "" {
					desc = media.Schema.Summary
				}
				setSchemaRecursive(media.Schema, desc)
			}
		}
	}

	// パスと操作を処理
	for _, pathItem := range parsedSpec.Paths {
		for _, ope := range getOperations(pathItem) {
			setParameter(ope.Parameters)
			setRequestBody(ope.RequestBody)
		}
	}

	// コンポーネントを処理
	if parsedSpec.Components != nil {
		// パラメータを処理
		parameters := make([]*ogen.Parameter, 0, len(parsedSpec.Components.Parameters))
		for _, parameter := range parsedSpec.Components.Parameters {
			parameters = append(parameters, parameter)
		}
		setParameter(parameters)

		// リクエストボディを処理
		for _, body := range parsedSpec.Components.RequestBodies {
			setRequestBody(body)
		}

		// スキーマを処理
		for _, schema := range parsedSpec.Components.Schemas {
			setSchemaRecursive(schema, schema.Description)
		}
	}
}

// OpenAPI仕様からogenクライアントを生成
func generateClient(spec *ogen.Spec, basePath, packageName string) (*gen.Generator, error) {
	outputPath := path.Join(basePath, "client")
	// 中間ステップを省略して、オリジナルのYAMLファイルを直接使用
	// 出力ディレクトリを絶対パスに変換
	absOutputPath, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	g, err := gen.NewGenerator(spec, gen.Options{
		Generator: gen.GenerateOptions{
			Features: &gen.FeatureOptions{
				Enable: gen.FeatureSet{
					"paths/client": struct{}{},
					"ogen/otel":    struct{}{},
				},
				DisableAll: true,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build IR: %w", err)
	}
	switch files, err := os.ReadDir(absOutputPath); {
	case os.IsNotExist(err):
		if err := os.MkdirAll(absOutputPath, 0o750); err != nil {
			return nil, err
		}
	default:
		if err := cleanDir(absOutputPath, files); err != nil {
			return nil, fmt.Errorf("failed cleanDir: %w", err)
		}
	}

	fs := genfs.FormattedSource{
		// FIXME(tdakkota): write source uses imports.Process which also uses go/format.
		// 	So, there is no reason to format source twice or provide a flag to disable formatting.
		Format: false,
		Root:   absOutputPath,
	}
	if err := g.WriteSource(fs, packageName); err != nil {
		return nil, fmt.Errorf("failed write: %w", err)
	}
	return g, nil
}

// MCP Toolsを生成
func generateMCPTools(g *gen.Generator, outputPath string) error {
	// 各エンドポイントに対応するMCP Toolを生成
	toolsDir := filepath.Join(outputPath, "tools")

	// ディレクトリを作成
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return fmt.Errorf("failed to create tools directory: %w", err)
	}

	for _, operation := range g.Operations() {
		// MCPツールファイルを生成
		toolFilename := strings.ToLower(operation.Spec.OperationID) + "_tool.go"
		toolFilePath := filepath.Join(toolsDir, toolFilename)

		// Jenniferを使ってコードを生成
		if err := generateMCPToolWithJennifer(
			operation,
			toolFilePath,
		); err != nil {
			return fmt.Errorf("failed to generate tool for %s: %w", operation.Name, err)
		}
	}

	return nil
}

// Jenniferを使用してMCPツールコードを生成
func generateMCPToolWithJennifer(operation *ir.Operation, outputPath string) error {
	// パッケージパスを準備
	outputDir := filepath.Dir(outputPath)
	basePath := strings.TrimSuffix(outputDir, "/tools")
	modName := getModuleName()
	// クライアントパッケージへの参照
	oasClient := modName + "/" + basePath + "/client"
	// function
	functions := "github.com/nonchan7720/oas-mcp/functions"

	toolDescription := ""
	switch {
	case operation.Description != "":
		toolDescription = operation.Description
	case operation.Summary != "":
		toolDescription = operation.Summary
	case operation.Spec.Summary != "":
		toolDescription = operation.Spec.Summary
	}

	// ファイル作成
	f := jen.NewFile("tools")

	// ファイルコメント
	f.HeaderComment("Code generated by OpenAPI MCP generator. DO NOT EDIT.")

	// インポート
	f.ImportName("context", "context")
	f.ImportName("encoding/json", "json")
	f.ImportName(functions, "functions")
	f.ImportName(oasClient, "client")

	// 関数コメント
	f.Comment(fmt.Sprintf("%s is a MCP tool for %s", operation.Spec.OperationID, toolDescription))
	// パスパラメータ、クエリパラメータ、リクエストボディの処理
	hasPathParams := len(operation.PathParams()) > 0
	hasQueryParams := len(operation.QueryParams()) > 0
	hasParams := hasPathParams || hasQueryParams
	hasRequestBody := operation.Request != nil
	const (
		reqParams = "RequestParameter"
		reqBody   = "RequestBody"
		input     = "input"
	)
	inputFields := []jen.Code{}
	if hasParams {
		inputFields = append(inputFields,
			jen.Id(reqParams).Qual(oasClient, operation.Name+"Params").Op(
				fmt.Sprintf("`json:\"requestParameter\" mcpdescription:\"%s\"`", operation.Description),
			),
		)
	}
	if hasRequestBody {
		ope := ""
		if operation.Request.DoTakePtr() {
			ope = "*"
		}
		inputFields = append(inputFields,
			jen.Id(reqBody).Op(ope).Qual(oasClient, operation.Request.Type.Name).Op("`json:\"requestBody\"`"),
		)
	}
	// 関数定義
	f.Func().Id("New"+operation.Name+"Tool").Params(
		jen.Id("oasClient").Op("*").Qual(oasClient, "Client"),
	).Op("*").Qual(functions, "Tool").Block(
		jen.Return(
			jen.Qual(functions, "NewFunctionTool").Call(
				jen.Lit(operation.Name),
				jen.Lit(toolDescription),
				jen.Func().Params(
					jen.Id("ctx").Qual("context", "Context"),
					jen.Id(input).Struct(
						inputFields...,
					),
				).Params(
					jen.Any(),
					jen.Error(),
				).BlockFunc(func(g *jen.Group) {
					g.Line()
					requestArgs := []jen.Code{
						jen.Id("ctx"),
					}
					if hasRequestBody {
						requestArgs = append(requestArgs, jen.Id(input).Dot(reqBody))
					}
					if hasParams {
						requestArgs = append(requestArgs, jen.Id(input).Dot(reqParams))
					}
					// クライアントを呼び出す（リクエストボディ + パラメータ）
					g.Line()
					g.Comment("クライアントを使用してAPIを呼び出し")
					g.List(jen.Id("resp"), jen.Id("err")).Op(":=").Id("oasClient").Dot(operation.Name).Call(
						requestArgs...,
					)

					g.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Lit(""), jen.Id("err")),
					)
					g.Line()

					// レスポンスをJSON文字列に変換
					g.Comment("レスポンスをJSON文字列に変換")
					g.List(jen.Id("resultBytes"), jen.Id("err")).Op(":=").Qual("encoding/json", "Marshal").Call(jen.Id("resp"))
					g.If(jen.Id("err").Op("!=").Nil()).Block(
						jen.Return(jen.Lit(""), jen.Id("err")),
					)
					g.Line()
					g.Return(jen.String().Call(jen.Id("resultBytes")), jen.Nil())
				}),
			),
		),
	)

	// ファイルに保存
	return f.Save(outputPath)
}

// MCP Serverを生成
func generateMCPServer(g *gen.Generator, outputPath string) error {
	// サーバーディレクトリ
	serverDir := filepath.Join(outputPath, "server")

	// ディレクトリを作成
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		return fmt.Errorf("failed to create server directory: %w", err)
	}

	// ツール名を収集
	var toolNames []string
	for _, operation := range g.Operations() {
		toolNames = append(toolNames, operation.Name)
	}
	// サーバーファイルパス
	serverFilePath := filepath.Join(serverDir, "server.go")

	// Jenniferを使ってサーバーコードを生成
	return generateMCPServerWithJennifer(toolNames, serverFilePath)
}

// Jenniferを使用してMCPサーバーコードを生成
func generateMCPServerWithJennifer(toolNames []string, outputPath string) error {
	// パッケージパスを準備
	outputDir := filepath.Dir(outputPath)
	basePath := strings.TrimSuffix(outputDir, "/server")
	modName := getModuleName()
	// クライアントパッケージへの参照
	oasClient := modName + "/" + basePath + "/client"
	// toolsパッケージへの参照
	toolsPath := modName + "/" + basePath + "/tools"

	// ファイル作成
	f := jen.NewFile("server")

	// ファイルコメント
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

	// StartServer関数の内容を構築
	funcBody := []jen.Code{
		jen.Comment("シャットダウンハンドリング"),
		jen.List(jen.Id("ctx"), jen.Id("stop")).Op(":=").Qual("os/signal", "NotifyContext").Call(
			jen.Id("ctx"),
			jen.Qual("syscall", "SIGINT"),
			jen.Qual("syscall", "SIGTERM"),
		),
		jen.Defer().Id("stop").Call(),
		// クライアント初期化
		jen.Comment("クライアント初期化"),
		jen.List(jen.Id("client"), jen.Id("err")).Op(":=").Qual(oasClient, "NewClient").Call(
			jen.Qual("os", "Getenv").Call(jen.Lit("API_BASE_URL")),
		),
		jen.If(jen.Id("err").Op("!=").Nil()).Block(
			jen.Return(jen.Id("err")),
		),
		jen.Line(),
		// MCPサーバー初期化
		jen.Comment("MCPサーバー初期化"),
		jen.Id("mcpServer").Op(":=").Qual("github.com/mark3labs/mcp-go/server", "NewMCPServer").Call(
			jen.Id("name"),
			jen.Id("version"),
			jen.Id("opts").Op("..."),
		),
		jen.Comment("全ツールを登録"),
	}

	toolsFunc := make([]jen.Code, len(toolNames))
	// 各ツールの登録処理を関数本体に追加
	for idx, toolName := range toolNames {
		toolsFunc[idx] = jen.Qual(toolsPath, "New"+toolName+"Tool").Call(jen.Id("client")).Dot("ServerTool").Call()
	}

	// シャットダウン処理を関数本体に追加
	funcBody = append(funcBody,
		jen.Id("mcpServer").Dot("AddTools").Call(toolsFunc...),
		jen.Id("sse").Op(":=").Qual("github.com/mark3labs/mcp-go/server", "NewSSEServer").Call(
			jen.Id("mcpServer"),
		),
		jen.Line(),
		jen.Go().Func().Params().Block(
			jen.Qual("log/slog", "InfoContext").Call(jen.Id("ctx"), jen.Lit("Start mcp server")),
			jen.If(
				jen.Id("err").Op(":=").Id("sse").Dot("Start").Params(jen.Id("addr")),
				jen.Id("err").Op("!=").Nil().Op("&&").Id("err").Op("!=").Qual("net/http", "ErrServerClosed"),
			).Block(
				jen.Qual("log/slog", "Error").Call(jen.Lit("MCP server Shutdown.")),
			),
		).Call(),
		jen.Op("<-").Id("ctx").Dot("Done").Call(),
		jen.Id("stop").Call(),
		jen.Qual("log/slog", "InfoContext").Params(jen.Id("ctx"), jen.Lit("Shutdown mcp server")),
		jen.Return(jen.Nil()),
	)

	// StartServer関数を追加
	f.Comment("StartServer starts the MCP server with all generated tools")
	f.Func().Id("StartServer").Params(
		jen.Id("ctx").Qual("context", "Context"),
		jen.Id("name"),
		jen.Id("version"),
		jen.Id("addr").String(),
		jen.List(jen.Id("opts").Op("...").Qual("github.com/mark3labs/mcp-go/server", "ServerOption")),
	).Error().Block(funcBody...)

	// ファイルに保存
	return f.Save(outputPath)
}

// PathItemから操作を取得するヘルパー関数
func getOperations(pathItem *ogen.PathItem) map[string]*ogen.Operation {
	operations := make(map[string]*ogen.Operation)

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

	log.Printf("go.mod not found")
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
