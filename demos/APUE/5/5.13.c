#include "apue.h"
#include <errno.h>

void make_temp(char *template);

int
main(void) {
    char good_template[] = "/tmp/dirxxxxx"; /*right way*/
    char *bad_template = "/tmp/dirxxxxx"; /*wrong way*/

    printf("trying to first create temp file...\n");
    make_temp(good_template);
    printf("trying to second create temp file...\n");
    make_temp(bad_template);
    exit(0);
}

void
make_temp(char *template) {
    int fd;
    struct stat sbuf;

    printf("template :%s\n",template);

    if ((fd = mkstemp(template)) < 0)
        err_sys("can't create temp file");

    printf("temp file name=%s\n",template);
    close(fd);

    if (stat(template,&sbuf) < 0) {
        if (errno == ENOENT)
            printf("file doesn't exist\n");
        else
            err_sys("stat failed");
    } else {
        printf("file exists\n");
        unlink(template);
    }
}