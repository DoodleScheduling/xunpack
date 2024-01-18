# Unpack manifests from crossplane resources
[![release](https://github.com/doodlescheduling/xunpack/actions/workflows/release.yaml/badge.svg)](https://github.com/doodlescheduling/xunpack/actions/workflows/release.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/doodlescheduling/xunpack)](https://goreportcard.com/report/github.com/doodlescheduling/xunpack)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/xunpack/badge)](https://api.securityscorecards.dev/projects/github.com/DoodleScheduling/xunpack)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/xunpack/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/xunpack?branch=master)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/xunpack)](https://artifacthub.io/packages/search?repo=xunpack)

This small utitily extrats manifests from crossplane packages
as well as converts any CompositeResourceDefinitions into CustomResourceDefinitions.

Crossplane packages are installed at runtime and any crossplane manifests are only available within the cluster.
The same applies for CompositeResourceDefinitions. Any CompositeResourceDefinitions are only installed at runtime.
This makes is hard to validate crossplane providers and/or custom crossplane resources before runtime.
However with this tool these manifests are available beforehand and resources can be validated in ci pipelines.

## Installation

### Brew
```
brew tap doodlescheduling/xunpack
brew install xunpack
```

### Docker
```
docker pull ghcr.io/doodlescheduling/xunpack:v0
```

## Arguments

| Flag  | Env | Default | Description |
| ------------- | ------------- | ------------- | ------------- |
| `--file`  | `IFILE` | `/dev/stdin` | Path to input |
| `--workers`  | `WORKERS`  | `Number of CPU cores` | Number of workers to process the manifest |
| `--fail-fast`  | `FAIL_FAST` | `false` | Exit early if an error occured |
| `--allow-failure`  | `ALLOW_FAILURE` | `false` | Do not exit > 0 if an error occured |
| `--output`  | `OUTPUT` | `/dev/stdout` | Path to output file |


## Github Action

This app works also great on CI, in fact this was the original reason why it was created.

### Example usage

```yaml
name: xunpack
on:
- pull_request

jobs:
  build:
    strategy:
      matrix:
        cluster: [staging, production]

    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@24cb9080177205b6e8c946b17badbe402adc938f # v3.4.0
    - uses: docker://ghcr.io/doodlescheduling/xunpack:v0
      env:
        PATHS: ./${{ matrix.cluster }}
        OUTPUT: /dev/null
```

### Advanced example

While a simple gitops pipeline just verifies if kustomizations can be built and HelmReleases installed a more advanced pipeline
includes follow-up validations like kyverno tests, kubeval validations or kubeaudit tests.

```yaml
name: xunpack
on:
- pull_request

jobs:
  build:
    strategy:
      matrix:
        cluster: [staging, production]

    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@24cb9080177205b6e8c946b17badbe402adc938f # v3.4.0
    - uses: docker://ghcr.io/doodlescheduling/xunpack:v0
      env:
        PATHS: ./${{ matrix.cluster }}
        WORKERS: "50"
        OUTPUT: ./build.yaml
    - name: Setup kubeconform
      shell: bash
      run: |
        curl -L -v --fail https://github.com/yannh/kubeconform/releases/download/v0.6.1/kubeconform-linux-amd64.tar.gz -o kubeconform.tgz
        tar xvzf kubeconform.tgz
        sudo mv kubeconform /usr/bin/
    - name: Setup openapi2jsonschema
      shell: bash
      run: |
        curl -L -v --fail https://raw.githubusercontent.com/yannh/kubeconform/v0.6.2/scripts/openapi2jsonschema.py -o openapi2jsonschema.py
        sudo mv openapi2jsonschema.py /usr/bin/openapi2jsonschema
        sudo chmod +x /usr/bin/openapi2jsonschema
    - name: Setup yq
      uses: chrisdickinson/setup-yq@3d931309f27270ebbafd53f2daee773a82ea1822 #v1.0.1
      with:
        yq-version: v4.24.5
    - name: Convert CRD to json schemas
      shell: bash
      run: |
        echo "openapi2jsonschema ./build.yaml"
        mkdir "schemas"
        cat $m | yq -e 'select(.kind == "CustomResourceDefinition")' > schemas/crds.yaml
        pip install pyyaml
        openapi2jsonschema schemas/*.yaml
    - name: Run conform
      shell: bash
      env: 
        KUBERNETES_VERSION: "${{ inputs.kubernetes-version }}"
      run: |
        echo "kubeconform $m"
        cat ./build.yaml | kubeconform -kubernetes-version $KUBERNETES_VERSION -schema-location default -schema-location "schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json" --skip CustomResourceDefinition,APIService --strict --summary
    - name: Setup kyverno
      shell: bash
      run: |
        curl -LO --fail https://github.com/kyverno/kyverno/releases/download/v1.7.2/kyverno-cli_v1.7.2_linux_x86_64.tar.gz
        tar -xvf kyverno-cli_v1.7.2_linux_x86_64.tar.gz
        sudo cp kyverno /usr/local/bin/
    - name: Test kyverno policies
      shell: bash
      run: |
        kyverno apply kyverno-policies -r ./build.yaml
```
