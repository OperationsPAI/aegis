FROM rayproject/ray:2.56.0-py313

USER root

RUN mkdir -p /etc/pip && \
    printf '[global]\nindex-url = https://mirrors.aliyun.com/pypi/simple/\ntrusted-host = mirrors.aliyun.com\n' > /etc/pip/pip.conf && \
    pip install --no-cache-dir pyarrow boto3 s3fs fsspec duckdb

USER ray
