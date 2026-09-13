//go:build generate

package bpf

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -type func_event -type arg_data -cflags "-I/usr/include/x86_64-linux-gnu -mllvm -bpf-stack-size=2048" bpf uprobe.c
