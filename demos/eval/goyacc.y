// goyacc.y - yacc 语法分析器规范文件
// 生成文件：y.go（不要手动编辑）
// 语法规则：定义数学表达式的语法结构

%{
package eval
%}

// 联合体定义，用于在解析过程中传递值
%union {
    value uint64  // 数值
    ast *ast      // AST 节点
}

// 非终结符类型
%type  <ast> expression

// 终结符类型
%token <value> NUMBER

// 运算符优先级（从低到高）：
// 1. + -（左结合）
// 2. * / % ^（左结合）
%left '+' '-'
%left '*' '/' '%' '^'

// 起始规则
%start top

%%

// 顶层规则：解析完成后将 AST 存储到 lexer 中
top        :expression
                {
                    if l, ok := yylex.(*simpleLex); ok {
                        l.ast = $1
                    }
                }
                ;

// 表达式规则：
// - 括号分组
// - 二元运算（+ - * / % ^）
// - 数值字面量
expression :'(' expression ')'
           {$$ = $2}
           | expression '+' expression
           {$$ = genOpNode('+',$1,$3)}
           | expression '-' expression
           {$$ = genOpNode('-',$1,$3)}
           | expression '*' expression
           {$$ = genOpNode('*',$1,$3)}
           | expression '/' expression
           {$$ = genOpNode('/',$1,$3)}
           | expression '%' expression
           {$$ = genOpNode('%',$1,$3)}
           | expression '^' expression
           {$$ = genOpNode('^',$1,$3)}
           | NUMBER
           {$$ = genValueNode($1)}
           ;
%%
