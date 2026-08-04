# Spring Boot Maven Proxy Smoke

This project resolves a broad Spring Boot dependency graph exclusively through
the local Artifact Gateway Maven Proxy.

```sh
export GATEWAY_MAVEN_PROXY_URL=http://127.0.0.1:18080/maven/maven-central-proxy
export GATEWAY_MAVEN_USERNAME=resolver
export GATEWAY_MAVEN_PASSWORD='<resolver token>'
mvn --batch-mode --settings settings.xml -Dmaven.repo.local=/tmp/artifact-gateway-m2 clean test
```

Run the command a second time with a fresh local Maven directory to verify that
the Gateway serves the dependency graph from its read-through cache.
