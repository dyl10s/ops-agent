<#
 Copyright 2025 Google LLC

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
#>

Set-PSDebug -Trace 1
$ErrorActionPreference = 'Stop'
$global:ProgressPreference = 'SilentlyContinue'

# Invokes the first argument (expected to be an external program) and passes it
# the rest of the arguments. Throws an error if the program finishes with a
# nonzero exit code.
#   Example: Invoke-Program git submodule update --init
function Invoke-Program() {
  $outpluserr = cmd /c $Args 2`>`&1
  if ( $LastExitCode -ne 0 ) {
    throw "failed: $Args, output: $outpluserr"
  }
  return $outpluserr
}

$tag = 'build'
$name = 'build-result'

# Try to disable Windows Defender antivirus for improved build speed.
# Sometimes it seems that Defender is already disabled and it fails with
# an error like "Set-MpPreference : Operation failed with the following error: 0x800106ba"
Set-MpPreference -Force -DisableRealtimeMonitoring $true -ErrorAction Continue
# Try to disable Windows Defender firewall for improved build speed.
Set-NetFirewallProfile -Profile Domain,Public,Private -Enabled False -ErrorAction Continue

$gitOnBorgLocation = "$env:KOKORO_ARTIFACTS_DIR/git/unified_agents"
if (Test-Path -Path $gitOnBorgLocation) {
  Set-Location $gitOnBorgLocation
}
else {
  Set-Location "$env:KOKORO_ARTIFACTS_DIR/github/unified_agents"
}

# Record OPS_AGENT_REPO_HASH so that we can later run tests from the
# same commit that the agent was built from. This only applies to the
# build+test flow for release builds, not the GitHub presubmits.
$hash = Invoke-Program git -C . rev-parse HEAD

# Set variables from the VERSION file. Currently this is only PKG_VERSION.
Get-Content VERSION | Where-Object length | ForEach-Object { Invoke-Expression "`$env:$_" };

# Write OPS_AGENT_REPO_HASH and PACKAGE_VERSION into Sponge custom config variables file.
Write-Output @"
OPS_AGENT_REPO_HASH,$hash
PACKAGE_VERSION,$env:PKG_VERSION
"@ |
  Out-File -FilePath "$env:KOKORO_ARTIFACTS_DIR/custom_sponge_config.csv" -Encoding ascii

Invoke-Program git submodule update --init
$artifact_registry='us-docker.pkg.dev'
Invoke-Program docker-credential-gcr configure-docker --registries="$artifact_registry"
$arch = Invoke-Program docker info --format '{{.Architecture}}'
$suffix = ''
if ($env:KOKORO_JOB_TYPE -eq 'RELEASE') {
  # There's some difference between release builds and other kinds of builds
  # that makes release builds slow unless they use a separate cache. See b/318538879.
  $suffix = '-release'
}
$cache_location="${artifact_registry}/stackdriver-test-143416/google-cloud-ops-agent-build-cache/ops-agent-cache:windows-${arch}${suffix}"
Invoke-Program docker pull $cache_location
Invoke-Program docker build --cache-from="${cache_location}" -t $tag -f './Dockerfile.windows' .
Invoke-Program docker create --name $name $tag
Invoke-Program docker cp "${name}:/work/out" $env:KOKORO_ARTIFACTS_DIR


# Tell our continuous build and release builds to update the cache.
# Our presubmits do not write to any kind of cache, for example a per-PR cache,
# because the push takes a few minutes and adds little value over just using
# the continuous build's cache.
if (($env:KOKORO_ROOT_JOB_TYPE -eq 'CONTINUOUS_INTEGRATION') -or ($env:KOKORO_JOB_TYPE -eq 'RELEASE')) {
  Invoke-Program docker image tag $tag $cache_location
  Invoke-Program docker push $cache_location
}

# Copy the .goo file from $env:KOKORO_ARTIFACTS_DIR/out to $env:KOKORO_ARTIFACTS_DIR/result.
# The .goo file is the installable package that is distributed to customers.
New-Item -Path $env:KOKORO_ARTIFACTS_DIR -Name 'result' -ItemType 'directory'
Move-Item -Path "$env:KOKORO_ARTIFACTS_DIR/out/*.goo" -Destination "$env:KOKORO_ARTIFACTS_DIR/result"
# Copy the .pdb and .dll files from $env:KOKORO_ARTIFACTS_DIR/out/bin to $env:KOKORO_ARTIFACTS_DIR/result.
# The .pdb and .dll files are saved so the team can use them in the event that we have to debug this Ops Agent build. 
# They are not distributed to customers.
Move-Item -Path "$env:KOKORO_ARTIFACTS_DIR/out/bin/*.pdb" -Destination "$env:KOKORO_ARTIFACTS_DIR/result"
Move-Item -Path "$env:KOKORO_ARTIFACTS_DIR/out/bin/*.dll" -Destination "$env:KOKORO_ARTIFACTS_DIR/result"
# Copy Ops Agent UAP Plugin tarball to the result directory.
(Get-FileHash -Path "$env:KOKORO_ARTIFACTS_DIR/out/bin/google-cloud-ops-agent-plugin*.tar.gz" -Algorithm SHA256).Hash.ToLower() | Out-File -FilePath "$env:KOKORO_ARTIFACTS_DIR/result/google-cloud-ops-agent-plugin-sha256.txt" -Encoding ascii
Move-Item -Path "$env:KOKORO_ARTIFACTS_DIR/out/bin/google-cloud-ops-agent-plugin*.tar.gz" -Destination "$env:KOKORO_ARTIFACTS_DIR/result"

# If Kokoro is being triggered by Louhi, then Louhi needs to be able to
# reconstruct the path where the artifacts are placed. Louhi does not have
# access to dynamic parameters generated by Kokoro, but it does supply its own.
# So for Louhi, we will upload to an additional location
if ($env:_LOUHI_TAG_NAME -ne $null) {
  # Example value: louhi/2.46.0/abcdef/windows/x86_64/start
  $louhi_tag_components = ${env:_LOUHI_TAG_NAME}.Split("/")
  $ver=$louhi_tag_components[1]
  $ref=$louhi_tag_components[2]
  $target=$louhi_tag_components[3]
  $arch=$louhi_tag_components[4]
  $gcs_bucket="gs://${env:_STAGING_ARTIFACTS_PROJECT_ID}-ops-agent-releases/${ver}/${ref}/${target}/${arch}/"
  gsutil cp "$env:KOKORO_ARTIFACTS_DIR/result/*.goo"  "${gcs_bucket}"
  gsutil cp "$env:KOKORO_ARTIFACTS_DIR/result/google-cloud-ops-agent-plugin*.tar.gz"  "${gcs_bucket}"
  gsutil cp "$env:KOKORO_ARTIFACTS_DIR/result/google-cloud-ops-agent-plugin-sha256.txt"  "${gcs_bucket}"
}
