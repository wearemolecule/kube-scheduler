FROM alpine
RUN apk --update add ca-certificates
COPY kube-scheduler /opt/
ENTRYPOINT ["/opt/kube-scheduler"]
