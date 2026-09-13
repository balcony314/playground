#include "apue.h"
#include <setjmp.h>

static void f1(int,int,int,int);
static void f2(void);

static jmp_buf jmp_buffer;

static int glob_val;

int
main(void)
{
	int auto_val;
	register int regi_val;
	volatile int vola_val;
	static int stat_val;

	glob_val = 1; auto_val = 2; regi_val = 3;stat_val = 5;vola_val = 7;

	printf("[0] - glob_val:%d,auto_val:%d,regi_val:%d,stat_val:%d,vola_val:%d\n",glob_val,auto_val,regi_val,stat_val,vola_val);

	if (setjmp(jmp_buffer) != 0 ) {

		printf("[3] - glob_val:%d,auto_val:%d,regi_val:%d,stat_val:%d,vola_val:%d\n",glob_val,auto_val,regi_val,stat_val,vola_val);

		printf("game over,0\n");

		exit(0);
	}


	glob_val = 100; auto_val = 200; regi_val = 300;stat_val = 500;vola_val = 700;

	f1(auto_val,regi_val,stat_val,vola_val);

	printf("game over,1\n");

	exit(0);
}

static void
f1(int i,int j,int k,int l)
{
	printf("[1] - glob_val:%d,auto_val:%d,regi_val:%d,stat_val:%d,vola_val:%d\n",glob_val,i,j,k,l);
	f2();
}

static void
f2(void)
{
	longjmp(jmp_buffer,1);
}
