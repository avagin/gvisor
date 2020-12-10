FROM fedora:32

RUN dnf install -y gcc make golang git gcc-g++ glibc-static.x86_64 vim strace gdb

ENV GOPATH /go/

WORKDIR  /go/src/github.com/google/syzkaller

RUN git init . && git remote add origin https://github.com/google/syzkaller && git fetch origin && git checkout origin/master && make

