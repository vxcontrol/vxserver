GOOS=linux GOARCH=386 P=linux32 LF="-Wl,--whole-archive" LD="-Wl,--no-whole-archive -pthread -lluajit -lm -ldl -lstdc++" T="vxserver" ./build.sh
