ARG RUNTIME_IMAGE
FROM ${RUNTIME_IMAGE}
COPY bin/ /opt/clusterforge/platform/
COPY entrypoint.sh /usr/local/bin/clusterforge-test-entrypoint
COPY healthcheck.sh /usr/local/bin/clusterforge-test-healthcheck
COPY inventory.ini /etc/ansible/inventory.ini
COPY smoke.yml /opt/clusterforge-test/smoke.yml
COPY deployment.json /opt/clusterforge/platform/deployment.json
RUN chmod 0755 /usr/local/bin/clusterforge-test-entrypoint /usr/local/bin/clusterforge-test-healthcheck /opt/clusterforge/platform/newplatform /opt/clusterforge/platform/clusterforge-backup /opt/clusterforge/platform/clusterforge-job
ENV NEWPLATFORM_ADDR=0.0.0.0:8080 NEWPLATFORM_DB_PATH=/var/lib/clusterforge-test/platform/platform.db NEWPLATFORM_ANSIBLE_BIN=/opt/ansible/bin/ansible-playbook NEWPLATFORM_RUN_ROOT=/var/lib/clusterforge-test/platform/runs NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS=/var/lib/clusterforge-test/platform/playbooks NEWPLATFORM_IMAGE_BUILD_ROOT=/var/lib/clusterforge-test/platform/image-builds NEWPLATFORM_SEED_PROFILE=identities NEWPLATFORM_SSH_KNOWN_HOSTS=/root/.ssh/known_hosts CLUSTERFORGE_RUN_ARCHIVE_DIR=/var/lib/clusterforge-test/platform/run-archives CLUSTERFORGE_BACKUP_ENABLED=false
EXPOSE 22 8080
ENTRYPOINT ["/usr/local/bin/clusterforge-test-entrypoint"]
