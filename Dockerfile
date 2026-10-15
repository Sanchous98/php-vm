# Стенд PHP-VM: Go 1.27.1 + PHP 8.3 CLI + python3 + git (+ корпус php-src PHP-8.3).
#
# Сборка:
#   docker build -t phpvm-bench .
#
# A/B-перф-стенд (12 нагрузок, CPU-время, cur vs перф-референс 70d92dd):
#   docker run --rm -v "$PWD":/repo phpvm-bench          # N=3 (Linux/macOS)
#   docker run --rm -v "${PWD}:/repo" phpvm-bench        # PowerShell (Windows)
#   docker run --rm phpvm-bench                          # без монтирования —
#                                                        # по копии репо в образе
#   docker run --rm -v "$PWD":/repo phpvm-bench 5        # N=5
#
# Ручной режим (PHPT-прогоны и т.п.):
#   docker run --rm -it -v "$PWD":/repo phpvm-bench bash
#   # внутри: PHPT_DIR=/corpus/Zend/tests GOTOOLCHAIN=local \
#   #   go test -vet=off -run TestPHPTree -v ./internal/php/
FROM php:8.3-cli-bookworm

ARG GO_VERSION=1.27.1
ARG PHPSRC_BRANCH=PHP-8.3

RUN apt-get update && apt-get install -y --no-install-recommends \
        git python3 ca-certificates curl xz-utils make \
    && rm -rf /var/lib/apt/lists/*

# Go фиксированной версии: тулчейн в образе, при прогонах не скачивается.
ARG TARGETARCH
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
        amd64) GOARCH=amd64 ;; \
        arm64) GOARCH=arm64 ;; \
        *) echo "unsupported arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" -o /tmp/go.tgz; \
    tar -C /usr/local -xzf /tmp/go.tgz; \
    rm /tmp/go.tgz
ENV PATH="/usr/local/go/bin:${PATH}" \
    GOTOOLCHAIN=local \
    GOPATH=/go GOCACHE=/go/cache GOMODCACHE=/go/pkg/mod

# Корпус php-src (для PHPT-прогонов; перф-стенду не нужен).
RUN git clone --depth 1 --branch "${PHPSRC_BRANCH}" https://github.com/php/php-src.git /corpus

# php:8.3-cli кладёт интерпретатор в /usr/local/bin; bench.py ищет php в PATH,
# но оставляем и привычный путь.
RUN ln -sf /usr/local/bin/php /usr/bin/php

# Копия репозитория в образе: `docker run --rm phpvm-bench` работает без
# монтирования (Windows PowerShell и т.п.). Монтирование -v ...:/repo
# перекрывает эту копию. .git включён — bench/ab.sh делает git worktree
# для сборки перф-референса.
COPY . /repo

WORKDIR /repo
COPY bench/entry.sh /usr/local/bin/entry.sh
# CRLF-контекст сборки (Windows): шебанг 'bash\r' ломает запуск — нормализуем LF.
RUN chmod +x /usr/local/bin/entry.sh \
    && sed -i 's/\r$//' /usr/local/bin/entry.sh /repo/bench/ab.sh /repo/bench/bench.py /repo/bench/entry.sh \
    && chmod +x /repo/bench/*.sh
ENTRYPOINT ["entry.sh"]
