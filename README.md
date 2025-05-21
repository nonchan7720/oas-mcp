# OAS-MCP

OpenAPI仕様からModel Context Protocol（MCP）サーバーを自動生成するツール

## 概要

OAS-MCPは、OpenAPI仕様書（YAML/JSON）からModel Context Protocol（MCP）サーバーとツールを自動的に生成するGoアプリケーションです。OpenAPIの各エンドポイントはMCPツールとして利用可能になります。

## 機能

- OpenAPI仕様からGoクライアントコードを自動生成
- 生成したクライアントコードを利用したMCPサーバーの構築
- 各APIエンドポイントをMCPツールとして提供
- SSE (Server-Sent Events) を活用したリアルタイム通信

## 必要条件

- Go 1.24以上

## インストール方法

```bash
# リポジトリをクローン
git clone https://github.com/nonchan7720/oas-mcp.git
cd oas-mcp

# 依存関係のインストール
go mod download
```

## 使用方法

```bash
# OpenAPI仕様からクライアントコードを生成
go run cmd/main.go -path=./api/openapi.yaml -output=./pkg/client

# または、go:generateを使用
go generate ./...
```

## 主な依存ライブラリ

- [ogen-go/ogen](https://github.com/ogen-go/ogen) - OpenAPIからGoコードを生成
- [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) - MCPサーバー実装
- [dave/jennifer](https://github.com/dave/jennifer) - Goコード生成ライブラリ

## ライセンス

このプロジェクトはオープンソースソフトウェアとして提供されています。詳細については[LICENSE](./LICENSE)ファイルをご覧ください。
