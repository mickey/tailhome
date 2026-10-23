FROM --platform=$BUILDPLATFORM busybox:stable AS data
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/tailhome /usr/local/bin/tailhome
# Owned by nonroot so named volumes work without a chown.
COPY --from=data --chown=65532:65532 /data /data
ENV TAILHOME_STATE_DIR=/data TAILHOME_OVERRIDES=/data/overrides.yaml
VOLUME /data
USER nonroot
ENTRYPOINT ["/usr/local/bin/tailhome"]
