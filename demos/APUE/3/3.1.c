#include <sys/types.h>
#include "apue.h"
int
main(void) {

	//int ret = lseek(STDIN_FILENO,0,SEEK_CUR);  
	//int ret = lseek(STDIN_FILENO,0,SEEK_SET);
	int ret = lseek(STDIN_FILENO,0,SEEK_END);
	printf("lseek:%d\n",ret);
	if (ret == -1) 
		printf("cannot lseek\n");
	else 
		printf("lseek ok\n");

	return 0;
}
