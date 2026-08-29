FROM gcr.io/distroless/static-debian13:nonroot
# TARGETARCH is set automatically when using BuildKit
ARG TARGETARCH
COPY .bin/linux-${TARGETARCH}/ddup /bin
CMD ["/bin/ddup"]
