# User-Mutator

The user-mutator will intercept the creation of deployment resources and mutate them in ways defined in the configmap that it's settings are retrieved from.

To deploy a very basic user-mutator which adds the proxy environment variables to deployments you can do the following.  This example is for the eduhelx-staging namespace.

The user-mutator can also add volumes to be mounted in pods.  There are YAMLs for this setup in the yamls directory.  eduhelx-data720 and eduhelx-chip690 are examples of adding volumes to pods.

## Clone the user-mutator github Repo
```
git clone https://github.com/helxplatform/user-mutator.git
```
Make sure you are on the 'master' branch.
```
git checkout master
```

## Create Secret for Proxy Credentials
Create secret that is used for adding environment variables to pods.  The secret needs to be encoded if there are any characters that need to be escaped (like in the proxy user's password).  This can be done in different ways.

### Use Browser's Developer Console
Use encodeURIComponent('password') in developer console of web browser to encode the password if there are symbols.

### Use node.js script at tools/encoder.js
You need to have node.js installed.  The script will prompt you for what text to encode.
```
cd tools
./encoder.js
```

### Create the Secret
```
NAMESPACE=[NAMESPACE TO DEPLOY USER_MUTATOR IN]
kubectl -n $NAMESPACE apply -f yamls/proxycreds-env-secret.yaml
```

## Create user-profiles Configmap for User-Mutator
Create configmap that is used for the user-mutator configuration for the namespace.
```
kubectl -n $NAMESPACE apply -f yamls/user-profiles-configmap.yaml
```

## Create New Environment File for the Namespace
Copy the config.env from the root of user-mutator source tree into ./envs and add namespace as suffix.
```
cp user-mutator/config.env ./envs/config-[NAMESPACE].env
```
Set these variables appropriately (change namespace name).
  - MUTATE_CONFIG=mutating-webhook-eduhelx-staging
  - WEBHOOK_NAMESPACE=eduhelx-staging
  - NAMESPACE_TO_MUTATE=eduhelx-staging
Remove or comment out the line with "VERSION" (unless you know you need to use a certain version).

## Deploy the User-Mutator to the Namespace
Change your current working directory to the root of the user-mutator code.
```
cd user-mutator
```

## Use the Makefile to Deploy the User-Mutator
```
make cnf=../envs/config-eduhelx-staging.env deploy-all
```

## Enable the User-Mutator to Run on objects created in the namespace.
For the User-Mutator to examine new objects in the namespace the namespace needs to have a label that enables it.
```
make cnf=../envs/config-eduhelx-staging.env enable-mutate-in-namespace
```

## Renew Certs when they Expire
```
make cnf=../envs/config-eduhelx-staging.env regenerate-ca-cert-key-and-update-cluster
```
