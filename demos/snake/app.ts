// 1. 定义一个接口，规范对象的结构
interface User {
  id: number;
  name: string;
  isAdmin?: boolean; // ? 表示可选属性
}

// 2. 使用接口约束变量类型
const currentUser: User = {
  id: 101,
  name: "张三",
};

// 3. 约束函数的参数类型和返回值类型
function getWelcomeMessage(user: User): string {
  if (user.isAdmin) {
    return `管理员 ${user.name}，欢迎回来！`;
  }
  return `你好，${user.name}！`;
}

// 4. 调用函数
const message = getWelcomeMessage(currentUser);
console.log(message); // 输出: 你好，张三！

let foo:string;

function xToString(num:number):string {
    return String(num);
}

let x = 1;
console.log(xToString(x)); // 输出: 你好，张三！


function simpleDecorator(
  value:any,
  context:any
) {
  console.log(`hi, this is ${context.kind} ${context.name}`);
  return value;
}

@simpleDecorator
@simpleDecorator
class A {} // "hi, this is class A"