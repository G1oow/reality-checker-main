//go:build !unix

// 非 unix 平台（仅用于本地开发与测试）不做文件描述符上限调整。
package cmd
