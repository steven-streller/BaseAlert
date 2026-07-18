# Kubernetes

Es gibt keine fertigen Manifeste im Repo – BaseAlert ist ein einzelner Pod mit
einer kleinen PVC für die SQLite-Datenbank, das lässt sich leicht in ein
bestehendes Manifest- oder GitOps-Setup (Flux, Argo CD, ...) einbauen.

Beispiel zum Anpassen (Namespace, Storage-Class, Image-Tag, Hostname etc. auf
die eigene Umgebung übertragen):

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: basealert
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: basealert-data
  namespace: basealert
spec:
  accessModes:
    - ReadWriteOnce
  # k3s liefert "local-path" standardmäßig mit; sonst longhorn, nfs-subdir-*, ...
  storageClassName: local-path
  resources:
    requests:
      storage: 256Mi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: basealert
  namespace: basealert
spec:
  replicas: 1
  strategy:
    type: Recreate # SQLite auf einem ReadWriteOnce-Volume verträgt keine 2 Pods
  selector:
    matchLabels:
      app: basealert
  template:
    metadata:
      labels:
        app: basealert
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        # Macht die PVC für GID 1000 gruppenbeschreibbar - das Image bringt
        # selbst einen UID/GID-1000-User mit (appuser), besitzt aber nicht
        # automatisch, was der jeweilige Provisioner zurückgibt.
        fsGroup: 1000
      # Nur nötig, falls das ghcr.io/<owner>/basealert Package privat ist:
      #   kubectl create secret docker-registry ghcr-creds -n basealert \
      #     --docker-server=ghcr.io --docker-username=<gh-user> --docker-password=<gh-pat>
      # imagePullSecrets:
      #   - name: ghcr-creds
      containers:
        - name: basealert
          image: ghcr.io/steven-streller/basealert:latest
          ports:
            - containerPort: 8000
          env:
            - name: TZ
              value: Europe/Berlin
            - name: BASEALERT_DB_PATH
              value: /app/data/basealert.db
            - name: REGISTRATION_ENABLED
              value: "true"
            # Ohne dieses Secret generiert die App bei jedem Container-Start
            # einen zufälligen Schlüssel - das meldet nach jedem Neustart alle ab.
            # kubectl create secret generic basealert-secrets -n basealert \
            #   --from-literal=session-secret-key=$(python3 -c "import secrets; print(secrets.token_hex(32))")
            - name: SESSION_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: basealert-secrets
                  key: session-secret-key
                  optional: true
          volumeMounts:
            - name: data
              mountPath: /app/data
          resources:
            requests:
              cpu: 50m
              memory: 96Mi
            limits:
              cpu: 300m
              memory: 256Mi
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8000
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8000
            initialDelaySeconds: 15
            periodSeconds: 30
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: basealert-data
---
apiVersion: v1
kind: Service
metadata:
  name: basealert
  namespace: basealert
spec:
  selector:
    app: basealert
  ports:
    - port: 8000
      targetPort: 8000
```

Ressourcen-Bedarf ist minimal: im Leerlauf ~70 MB RAM, kurze CPU-Spitzen nur
während eines Scrape-Laufs.

## Erreichbarkeit

Der `Service` oben ist `ClusterIP`. Für Zugriff von außen entweder

```bash
kubectl port-forward -n basealert svc/basealert 8000:8000
```

nutzen, oder eine `Ingress`-Ressource mit echtem Hostnamen ergänzen (k3s
bringt dafür Traefik mit):

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: basealert
  namespace: basealert
spec:
  rules:
    - host: basealert.example.internal
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: basealert
                port:
                  number: 8000
```

## Non-root

Das Image läuft als `appuser` (UID/GID 1000). Der `securityContext` oben
(`runAsUser`/`runAsGroup`/`fsGroup: 1000`) ist nötig, damit die PVC für diese
UID beschreibbar gemountet wird – ohne passenden `fsGroup` bzw. ohne einen zur
UID passenden `/etc/passwd`-Eintrag im Image zeigt sich das als
"I have no name!" in einer interaktiven Shell.
