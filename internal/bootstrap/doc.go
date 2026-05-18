// Package bootstrap 把 config 翻译成进程级运行时资源（Registry 模式）。
// API / Worker / Migrate 三个进程各调对应的 InitXxx 装配自己需要的字段。
package bootstrap
