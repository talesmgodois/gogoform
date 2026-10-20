# Image of the `backup` service of docker-compose.prod.yml: pg_dump of the
# same major version as the database, plus the AWS CLI for the optional upload
# to S3-compatible storage. The script itself (scripts/backup.sh) is mounted.
FROM postgres:18-alpine
RUN apk add --no-cache aws-cli
