#!/usr/bin/env python

import argparse
import string
import random
import base64
from kubernetes import client, config

def get_current_namespace():
    """Retrieve the current namespace from the Kubernetes context."""
    try:
        config.load_kube_config()  # Load the user's kubeconfig
        context = config.list_kube_config_contexts()[1]  # Get current context
        return context.get("context", {}).get("namespace", "default")
    except Exception as e:
        print(f"Failed to get current namespace: {e}")
        return "default"

def generate_password(length=16):
    """Generate a random password with letters, digits, and punctuation."""
    characters = string.ascii_letters + string.digits + string.punctuation
    return ''.join(random.choice(characters) for _ in range(length))

def secret_exists(v1, namespace, secret_name):
    """Check if the secret already exists in the namespace."""
    try:
        v1.read_namespaced_secret(secret_name, namespace)
        return True
    except client.exceptions.ApiException as e:
        if e.status == 404:
            return False
        print(f"Error checking if secret exists: {e}")
        return False

def create_or_update_secret(namespace, secret_name, password, force):
    """Create a Kubernetes secret if it doesn't exist, or overwrite it if --force is used."""
    config.load_kube_config()  # Load kubeconfig for authentication
    v1 = client.CoreV1Api()

    password_b64 = base64.b64encode(password.encode()).decode()
    secret_data = {"password": password_b64}

    if secret_exists(v1, namespace, secret_name):
        if force:
            print(f"Secret '{secret_name}' already exists. Overwriting it due to --force flag.")
            secret = v1.read_namespaced_secret(secret_name, namespace)
            secret.data = secret_data
            v1.replace_namespaced_secret(secret_name, namespace, secret)
            print(f"Secret '{secret_name}' successfully updated in namespace '{namespace}'.")
        else:
            print(f"Secret '{secret_name}' already exists in namespace '{namespace}'. Use --force to overwrite.")
    else:
        print(f"Creating new secret '{secret_name}' in namespace '{namespace}'.")
        secret = client.V1Secret(
            api_version="v1",
            kind="Secret",
            metadata=client.V1ObjectMeta(name=secret_name, namespace=namespace),
            type="Opaque",
            data=secret_data
        )
        v1.create_namespaced_secret(namespace, secret)
        print(f"Secret '{secret_name}' successfully created in namespace '{namespace}'.")

def main():
    parser = argparse.ArgumentParser(description="Store a password in a Kubernetes secret using Python client.")
    parser.add_argument("-n", "--namespace", default=get_current_namespace(), help="Target Kubernetes namespace.")
    parser.add_argument("-p", "--password", default=None, help="Password to store in the secret.")
    parser.add_argument("-s", "--secret-name", default="user-mutator-ldap-password", help="Name of the Kubernetes secret.")
    parser.add_argument("-f", "--force", action="store_true", help="Overwrite the secret if it already exists.")

    args = parser.parse_args()
    password = args.password if args.password else generate_password()

    create_or_update_secret(args.namespace, args.secret_name, password, args.force)

if __name__ == "__main__":
    main()
