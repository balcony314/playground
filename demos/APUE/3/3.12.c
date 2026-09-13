#include "apue.h"
#include <fcntl.h>

void 
set_fl(int fd,int flags) {
	int val;
	
	if ((val = fcntl(fd,F_GETFL,0)) < 0)
		err_sys("fcntl F_GETFL error");

	val |= flags; //turn on flags
	//val &= ~flags; //turn flags off
	
	if (fcntl(fd,F_SETFL,val) < 0) 
		err_sys("fcntl F_SETFL error");
	
} 

int main() {

   set_fl(STDOUT_FILENO,O_SYNC);//每次write都要等待，数据写到磁盘上再返回
   printf("test\n");
   return 0;
}
