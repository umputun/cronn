FROM umputun/baseimage:buildgo-latest as build

ARG GIT_BRANCH
ARG GITHUB_SHA
ARG CI

ENV GOFLAGS="-mod=vendor"

ADD . /build/cronn
WORKDIR /build/cronn

RUN \
    if [ -z "$CI" ] ; then \
    echo "runs outside of CI" && version=$(/script/git-rev.sh); \
    else version=${GIT_BRANCH}-${GITHUB_SHA:0:7}-$(date +%Y%m%dT%H:%M:%S); fi && \
    echo "version=$version" && \
    go build -o cronn -ldflags "-X main.revision=${version} -s -w"


FROM umputun/baseimage:app-latest

COPY --from=build /build/cronn/cronn /srv/cronn
RUN chmod +x /srv/cronn

WORKDIR /srv

CMD ["/srv/cronn", "-f", "/srv/crontab"]
ENTRYPOINT ["/init.sh"]
