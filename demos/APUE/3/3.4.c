#include "apue.h"

#define BUFFSIZE 8192

int
main(void) {

	int n;
	char buf[BUFFSIZE];

	while( (n = read(STDIN_FILENO,buf,BUFFSIZE)) > 0 ){
		if (write(STDOUT_FILENO, buf ,n) != n) {
			printf("write error\n");
			return -1;
		}	
		printf("\nbuf:{%s}\n====================================================\n",buf);
	}

	printf("[%d]\n",n);

	if (n < 0) {
		printf("read error\n");
		return -1;
	}

	return 0;
}
