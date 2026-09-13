package eval

import (
	"errors"
	"fmt"
	"math"
)

// 预定义错误
var (
	AstIsEmptyErr error = errors.New("ast node is empty")   // AST 节点为空
	DivisorIs0Err error = errors.New("divisor is 0")        // 除数为 0
)

// ast 抽象语法树节点
type ast struct {
	tokenID   int    // 节点类型：NUMBER 表示数值，其他为运算符
	leftNode  *ast   // 左子节点
	rightNode *ast   // 右子节点
	value     uint64 // 当 tokenID 为 NUMBER 时存储数值
}

// genOpNode 创建运算符节点
func genOpNode(tokenID int, l, r *ast) *ast {
	return &ast{
		leftNode:  l,
		rightNode: r,
		tokenID:   tokenID,
	}
}

// genValueNode 创建数值节点
func genValueNode(val uint64) *ast {
	return &ast{
		tokenID: NUMBER,
		value:   val,
	}
}

// printlnAST 调试用：前序遍历打印 AST
func printlnAST(cur *ast) {
	if cur == nil {
		return
	}

	if cur.tokenID != NUMBER {
		fmt.Printf("%p,%+v:%c\n", cur, cur, cur.tokenID)
	} else {
		fmt.Printf("%p,%+v: uint\n", cur, cur)
	}

	printlnAST(cur.leftNode)
	printlnAST(cur.rightNode)
}

// eval 递归求值 AST，返回 int64 结果
func eval(cur *ast) (ret int64, err error) {
	if cur == nil {
		err = AstIsEmptyErr
		return
	}

	switch cur.tokenID {
	case NUMBER:
		ret = int64(cur.value)
	default:
		// 递归求值左右子树
		leftValue, er1 := eval(cur.leftNode)
		if er1 != nil {
			err = er1
			return
		}
		rightValue, er2 := eval(cur.rightNode)
		if er2 != nil {
			err = er2
			return
		}
		// 根据运算符计算结果
		switch cur.tokenID {
		case '+':
			ret = leftValue + rightValue
		case '-':
			ret = leftValue - rightValue
		case '*':
			ret = leftValue * rightValue
		case '/':
			if rightValue == 0 {
				err = DivisorIs0Err
				return
			}
			ret = leftValue / rightValue
		case '^':
			ret = int64(math.Pow(float64(leftValue), float64(rightValue)))
		case '%':
			ret = leftValue % rightValue
		}
	}
	return
}
