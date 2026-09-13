package eval

import (
	"fmt"
	"strconv"
)

// simpleLex 适配器，将词法分析器输出转换为 goyacc 解析器接口
type simpleLex struct {
	pos       int        // 当前 token 位置
	tokenList []*token   // 词法分析结果
	ast       *ast       // 语法分析结果（AST）
	err       error      // 错误信息
}

// Lex 实现 goyacc 的 yyLexer 接口，返回下一个 token 的 ID
func (s *simpleLex) Lex(lval *yySymType) (tokID int) {
	w := s.Scan()
	if w == nil {
		return
	}

	var err error

	switch w.id {
	case NON_NEGATIVE_INTEGER:
		lval.value, err = strconv.ParseUint(w.raw, 10, 64)
		tokID = NUMBER
	default:
		tokID = int(w.id)
	}

	if err != nil {
		s.err = err
	}

	return
}

// Scan 从 token 列表中获取下一个 token
func (s *simpleLex) Scan() *token {
	if s.pos >= len(s.tokenList) {
		return nil
	}

	tok := s.tokenList[s.pos]
	s.pos++
	return tok
}

// Error 实现 goyacc 的 yyLexer 接口，处理语法错误
func (s *simpleLex) Error(s1 string) {
	s.err = fmt.Errorf("%s", s1)
}

// Calc 计算数学表达式的公开 API
// 流程：输入字符串 -> 词法分析 -> 语法分析 -> AST 求值 -> 返回结果
func Calc(raw string) (vale int64, err error) {
	// 词法分析
	tokenList, err := lexicalAnalysis(raw)
	if err != nil {
		return
	}

	// 语法分析
	lex := &simpleLex{
		tokenList: tokenList,
	}
	yyParse(lex)

	if lex.err != nil {
		err = lex.err
		return
	}

	// AST 求值
	return eval(lex.ast)
}
