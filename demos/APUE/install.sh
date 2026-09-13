#!/bin/bash
set -e
#apt-get install libbsd-dev

tar -zxv -f apue.3e.tar.gz

cd ./apue.3e

make

cp ./include/apue.h /usr/local/include/ -f

cp lib/error.c /usr/include/ -f

cp ./lib/libapue.a /usr/local/lib/ -f

cd ..

rm apue.3e -r

echo "[install ok]"

#gcc *.c -lapue
