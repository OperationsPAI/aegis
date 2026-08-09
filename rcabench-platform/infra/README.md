# Internal Infrastructure

## JuiceFS

| Service       | URL                       | User       | Password   |
| :------------ | :------------------------ | :--------- | :--------- |
| redis         | <http://10.10.10.119:6379> |            |            |
| minio         | <http://10.10.10.119:9000> | minioadmin | minioadmin |
| minio console | <http://10.10.10.119:9001> | minioadmin | minioadmin |
| prometheus    | <http://10.10.10.119:9090> |            |            |
| grafana       | <http://10.10.10.119:3000> | minioadmin | minioadmin |

Please carefully operate the JuiceFS services. Losing data will cause many problems. If you are not sure, please ask the administrator for help.

Install JuiceFS client: <https://juicefs.com/docs/zh/community/getting-started/installation>

Mount JuiceFS to your machine:

```bash
sudo juicefs mount redis://10.10.10.119:6379/1 /mnt/jfs -d --cache-size=1024
```

Note that the cache size is set to 1024MiB instead of the default 100GiB. If your machine has enough disk space, you can set it to `--cache-size=102400` or other values.

Check if the mount was successful:

```bash
df -h
ls /mnt/jfs
```

Umount JuiceFS:

```bash
sudo juicefs umount /mnt/jfs
```
