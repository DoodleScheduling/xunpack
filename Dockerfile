FROM gcr.io/distroless/static:latest
WORKDIR /
COPY xunpack xunpack

ENTRYPOINT ["/xunpack"]
