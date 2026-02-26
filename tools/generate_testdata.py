#!/usr/bin/env python

import sys
from jinja2 import Environment, BaseLoader

# Python script to generate a parameterized ConfigMap and PVC using Jinja2 templating and write them to files

configmap_template_text = """
apiVersion: v1
kind: ConfigMap
metadata:
  name: user-features
  namespace: [[ namespace ]]
data:
  auto: |
    {
      "secretsFrom": [
        {
          "secretName": "global-sample-secret"
        }
      ],
      "configMapsFrom": [
        {
          "configMapName": "global-sample-configmap"
        }
      ]
    }
  [[ username ]].json: |
    {
      "secretsFrom": [
        {
          "secretName": "[[ username_lower ]]-sample-secret"
        }
      ],
      "configMapsFrom": [
        {
          "configMapName": "[[ username_lower ]]-sample-configmap"
        }
      ],
      "volumes": {
        "volumeMounts": [
          {
            "mountPath": "/mnt/test",
            "name": "test"
          },
          {
            "mountPath": "/mnt/config",
            "name": "config"
          }
        ],
        "volumeSources": [
          {
            "name": "test",
            "source": "pvc://[[ username_lower ]]-test-pvc"
          },
          {
            "name": "config",
            "source": "configmap://[[ username_lower ]]-volume-configmap"
          }
        ]
      }
    }
"""

secret_template_text1 = """
apiVersion: v1
kind: Secret
metadata:
  name: global-sample-secret
  namespace: [[ namespace ]]
data:
  example-key: YWJjMTIz  # 'abc123' base64 encoded
"""

secret_template_text2 = """
apiVersion: v1
kind: Secret
metadata:
  name: [[ username_lower ]]-sample-secret
  namespace: [[ namespace ]]
data:
  [[ username_lower ]]-example-key: YWJjMTIz  # 'abc123' base64 encoded
"""

pvc_template_text = """
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: [[ username_lower ]]-test-pvc
  namespace: [[ namespace ]]
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
"""

configmap_env_template_text1 = """
apiVersion: v1
kind: ConfigMap
metadata:
  name: global-sample-configmap
  namespace: [[ namespace ]]
data:
  APP_CONFIG: "production"
  DEBUG_LEVEL: "info"
  API_ENDPOINT: "https://api.example.com"
"""

configmap_env_template_text2 = """
apiVersion: v1
kind: ConfigMap
metadata:
  name: [[ username_lower ]]-sample-configmap
  namespace: [[ namespace ]]
data:
  USER_HOME: "/home/[[ username_lower ]]"
  USER_WORKSPACE: "/workspace/[[ username_lower ]]"
  CUSTOM_SETTING: "user-specific-value"
"""

configmap_volume_template_text = """
apiVersion: v1
kind: ConfigMap
metadata:
  name: [[ username_lower ]]-volume-configmap
  namespace: [[ namespace ]]
data:
  app.conf: |
    [settings]
    user=[[ username_lower ]]
    timestamp=now
  init.sh: |
    #!/bin/bash
    echo "Initializing for user [[ username_lower ]]"
"""

def generate_yaml(template_text, data, output_file):
    """
    Generates YAML with the given template and data using Jinja2 and writes it to a file.

    :param template_text: Template text for the YAML.
    :param data: Data to be used in the template.
    :param output_file: Path to the output file where the YAML will be written.
    :return: None
    """
    env = Environment(loader=BaseLoader(), variable_start_string='[[', variable_end_string=']]')
    template = env.from_string(template_text)
    yaml_content = template.render(data)
    
    with open(output_file, 'w') as file:
        file.write(yaml_content)

# Main execution
if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("Usage: generate_configmap.py <namespace> <username>")
        sys.exit(1)

    namespace = sys.argv[1]
    username = sys.argv[2]
    username_lower = username.lower()

    configmap_output_file = f"{namespace}_{username}_configmap.yaml"
    pvc_output_file = f"{namespace}_{username_lower}_pvc.yaml"
    secret1_output_file = f"{namespace}_global_secret.yaml"
    secret2_output_file = f"{namespace}_{username_lower}_secret.yaml"
    configmap_env1_output_file = f"{namespace}_global_sample_configmap.yaml"
    configmap_env2_output_file = f"{namespace}_{username_lower}_sample_configmap.yaml"
    configmap_volume_output_file = f"{namespace}_{username_lower}_volume_configmap.yaml"

    generate_yaml(configmap_template_text, {"namespace": namespace, "username": username, "username_lower": username_lower}, configmap_output_file)
    generate_yaml(pvc_template_text, {"namespace": namespace, "username_lower": username_lower}, pvc_output_file)
    generate_yaml(secret_template_text1, {"namespace": namespace}, secret1_output_file)
    generate_yaml(secret_template_text2, {"namespace": namespace, "username_lower": username_lower}, secret2_output_file)
    generate_yaml(configmap_env_template_text1, {"namespace": namespace}, configmap_env1_output_file)
    generate_yaml(configmap_env_template_text2, {"namespace": namespace, "username_lower": username_lower}, configmap_env2_output_file)
    generate_yaml(configmap_volume_template_text, {"namespace": namespace, "username_lower": username_lower}, configmap_volume_output_file)

    print(f"ConfigMap written to {configmap_output_file}")
    print(f"PVC written to {pvc_output_file}")
    print(f"Secret written to {secret1_output_file}")
    print(f"Secret written to {secret2_output_file}")
    print(f"ConfigMap (EnvFrom) written to {configmap_env1_output_file}")
    print(f"ConfigMap (EnvFrom) written to {configmap_env2_output_file}")
    print(f"ConfigMap (Volume) written to {configmap_volume_output_file}")
