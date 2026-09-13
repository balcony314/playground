package main

import (
	"fmt"
	"os"
	"time"
)

// Add 两个数相加
func Add(a, b int) int {
	return a + b
}

// Greet 打招呼
func Greet(name string) string {
	return fmt.Sprintf("Hello, %s.", name)
}

// Process 处理数据
func Process(data []byte) int {
	return len(data)
}

func main() {
	fmt.Println("目标程序已启动，PID:", os.Getpid())

	// 定期调用函数
	for i := 0; ; i++ {
		result := Add(i, i*2)
		fmt.Printf("Add(%d, %d) = %d\n", i, i*2, result)

		greeting := Greet(fmt.Sprintf("User-%d", i))
		fmt.Println(greeting)

		data := []byte(fmt.Sprintf("data-%d", i))
		size := Process(data)
		fmt.Printf("Process data size: %d\n", size)

		time.Sleep(time.Second)
	}
}
