#include "apue.h"
#include <fcntl.h>

int main(int argc,char *argv[]) {

    int val;
    printf("argc:%d,argv[0]:%s,argc[1]:%s\n",argc,argv[0],argv[1]);
    
    if (argc != 2 ) 
        err_quit("usage: a.out <descriptor#>");

    if ((val = fcntl(atoi(argv[1]), F_GETFL,O_RDONLY)) < 0)
        err_sys("fcntl error for fd %d",atoi(argv[1]));

    switch (val & O_ACCMODE) {
    case O_RDONLY:
        printf("read only");
        break;
    case O_WRONLY:
        printf("write only");
        break;
    case O_RDWR:
        printf("read write");
        break;
    default:
        err_dump("unknown acces mode");
    }


    if (val & O_APPEND) 
        printf(", append");
    if (val & O_NONBLOCK)
        printf(", nonblocking");
    if (val & O_SYNC)
        printf(", synchronnous writes");

    #if !defined(_POSIX_C_SOURCE) && defined(O_FSYNC) && (O_FSYNC != O_SYNC)
        if (val & O_FSYNC)
            printf(", synchronous writes");
    #endif

    putchar('\n');
    exit(0);
}
