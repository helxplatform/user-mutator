#!/usr/bin/env python

import sys
from jinja2 import Environment, BaseLoader

# Python script to generate a parameterized ConfigMap using Jinja2 templating and write it to a file

template_text = """
apiVersion: v1
kind: ConfigMap
metadata:
  name: user-features
  namespace: [[ namespace ]]
data:
  [[ username ]].json: |
    {
      "config": {
        "volumeMounts": [
          {
            "mountPath": "/mnt/test",
            "name": "test"
          }
        ],
        "volumeSources": [
          {
            "name": "test",
            "source": "pvc://[[ pvc_name ]]-test-pvc"
          }
        ]
      }
    }
"""

def generate_configmap(namespace, username, output_file):
    """
    Generates a ConfigMap YAML with the given parameters using Jinja2 and writes it to a file.

    :param namespace: Namespace for the ConfigMap.
    :param username: Username to be used in the data and as part of the PVC name.
    :param output_file: Path to the output file where the ConfigMap will be written.
    :return: None
    """
    pvc_name = username.lower()  # PVC name derived from username, always lowercase

    # Set up the Jinja2 environment with custom delimiters
    env = Environment(loader=BaseLoader(), variable_start_string='[[', variable_end_string=']]')
    template = env.from_string(template_text)

    # Render the template with the data
    configmap_yaml = template.render(namespace=namespace, username=username, pvc_name=pvc_name)

    # Write the result to the specified file
    with open(output_file, 'w') as file:
        file.write(configmap_yaml)

# Main execution
if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("Usage: generate_configmap.py <namespace> <username>")
        sys.exit(1)

    namespace = sys.argv[1]
    username = sys.argv[2]
    output_file = f"{namespace}_{username}_configmap.yaml"  # Constructed output filename

    generate_configmap(namespace, username, output_file)
    print(f"ConfigMap written to {output_file}")
