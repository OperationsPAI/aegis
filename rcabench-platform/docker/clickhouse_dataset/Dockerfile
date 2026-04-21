ARG BASE_IMAGE=10.10.10.240/library/rcabench-platform:latest
FROM ${BASE_IMAGE}

ENV TZ=Asia/Shanghai
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /app

COPY entrypoint.sh /entrypoint.sh

ARG ABNORMAL_START_VAR
ARG ABNORMAL_END_VAR
ARG NORMAL_START_VAR
ARG NORMAL_END_VAR
ENV ABNORMAL_START=${ABNORMAL_START_VAR}
ENV ABNORMAL_END=${ABNORMAL_END_VAR}
ENV NORMAL_START=${NORMAL_START_VAR}
ENV NORMAL_END=${NORMAL_END_VAR}

# Default opt-out for parquets that many benchmark systems never produce.
# valid() (in rcabench_platform…sdk.datasets.rcabench) honours this env var:
# parquets in this list are allowed to have zero rows (file must still exist
# and parse cleanly). Override per-task via container_versions.env_vars when
# a benchmark should enforce the full set. Currently histograms: the Google
# microservices-demo stack (onlineboutique) doesn't emit OTel histograms,
# whereas otel-demo's SDK does — so the relaxation is no-op for otel-demo.
ENV RCABENCH_OPTIONAL_EMPTY_PARQUETS="abnormal_metrics_histogram.parquet,normal_metrics_histogram.parquet"

ENTRYPOINT ["/entrypoint.sh"]
