# Dockerfile for publishing build to repo
FROM debian:buster-slim

RUN mkdir -p /opt/vxserver/bin
RUN mkdir -p /opt/vxserver/data
RUN mkdir -p /opt/vxserver/logs

ADD preparing.sh /opt/vxserver/bin/
ADD build/vxserver /opt/vxserver/bin/
ADD migrations /opt/vxserver/migrations

WORKDIR /opt/vxserver

RUN chmod +x /opt/vxserver/bin/preparing.sh
RUN /opt/vxserver/bin/preparing.sh

RUN apt update
RUN apt install -y ca-certificates
RUN apt clean

ENTRYPOINT ["/opt/vxserver/bin/vxserver"]
