#include <sys/types.h>
#include <sys/stat.h>
#include <fcntl.h>
#include "apue.h"

char buf1[] = "zxcvbnmabc";
char buf2[] = "1234567890";

int
main(void) {

	int fd;
	
	//fd = creat("file.hole",FILE_MODE);
	//等价
	fd = open("file.hole",O_WRONLY|O_CREAT|O_TRUNC,FILE_MODE);

	printf("creat ret:%d\n",fd);

	if (fd < 0) {
		printf("creat error");
		return -1;

	}
	//	
	int ret = write(fd,buf1,10);
	printf("write ret:%d\n",ret);

	if (ret != 10) {
		printf("buf1 write error");
		return -1;
		/*offset  now = 10*/
	}

	//
	int ret1 = lseek(fd,40,SEEK_SET);
	printf("lseek ret:%d\n",ret1);
	if (ret1 == -1) {
		printf("lseek error\n");
		return -1;
	} 
	/*now offset = 40*/

	int ret2 = write(fd,buf2,10);
	if (ret2 == -1) {
		printf("wirite error\n");
		return -1;
	}

	/*now offset = 50*/
	int ret3 = lseek(fd,0,SEEK_CUR);
	printf("lseek ret:%d\n",ret3);

	close(fd);
	return 0;
}
