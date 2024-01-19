FROM gcr.io/distroless/static:nonroot@sha256:112a87f19e83c83711cc81ce8ed0b4d79acd65789682a6a272df57c4a0858534
WORKDIR /
COPY xunpack xunpack

ENTRYPOINT ["/xunpack"]
