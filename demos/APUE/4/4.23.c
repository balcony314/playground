#include "apue.h"

int
main(int argc,char *argv[]) {
	
	if (chdir("/tmp") < 0)
		err_sys("chdir failed");
	printf("chdir ot /tmp succeeded\n");
	return 0;
}

