ARG BASE_IMAGE=docker.io/opspai/rcabench-platform:latest
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
# a benchmark should enforce the full set.
#
# Histograms: the Google microservices-demo stack (onlineboutique) doesn't
# emit OTel histograms, whereas otel-demo's SDK does — so the relaxation is
# a no-op for otel-demo.
# Sum metrics: DSB-family (hotelreservation, mediamicroservices, socialnetwork)
# and the Coherence/Helidon sockshop fork export via Prometheus/Jaeger, not
# OTLP, so OTel sum metrics are never produced. Otel-demo and onlineboutique
# (with the cluster-side kube-stack prometheus receiver) do emit sum metrics,
# so the relaxation is a no-op for them too. Overall effect: the full datapack
# still validates for non-OTel-native stacks while telemetry-complete stacks
# enforce the strict set naturally via the presence of real data.
ENV RCABENCH_OPTIONAL_EMPTY_PARQUETS="abnormal_metrics_histogram.parquet,normal_metrics_histogram.parquet,abnormal_metrics_sum.parquet,normal_metrics_sum.parquet"

ENTRYPOINT ["/entrypoint.sh"]
