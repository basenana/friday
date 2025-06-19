#!/bin/bash
#****************************************************************#
# ScriptName: sync.sh
#***************************************************************#
set -e

username=${ACR_USERNAME}
passwd=${ACR_TOKEN}
image_input=$1
platform=$2

REGIONS=(
	  registry.cn-hangzhou.aliyuncs.com
)

sync_image() {
  local registryName=$1
  local image=$2
  local tag=${3:-latest}
  local platform=$4
  if [ "$platform" == "amd64" ]; then platform=""; fi
  local platform_suffix=${platform:+-$platform}

  echo "Syncing image: $image, platform: ${platform:-amd64}"

  if [ -n "$platform" ]; then
    docker pull $registryName/$image:${tag} --platform=${platform}
    for REGION in ${REGIONS[@]};
    do
      echo "in ${REGION}"
      docker login --username=${username} --password=${passwd} ${REGION}
      docker tag $registryName/$image:${tag} ${REGION}/hdls/${image}:${tag}${platform_suffix}
      docker push ${REGION}/hdls/${image}:${tag}${platform_suffix}
      sleep 5
    done
  else
    docker pull $registryName/$image:${tag}
    for REGION in ${REGIONS[@]};
    do
      echo "in ${REGION}"
      docker login --username=${username} --password=${passwd} ${REGION}
      docker tag $registryName/$image:${tag} ${REGION}/hdls/${image}:${tag}
      docker push ${REGION}/hdls/${image}:${tag}
      sleep 5
    done
  fi
}

parse_image_name() {
    local image="$1"

    local registry_name="docker.io"
    local image_name=""
    local tag="latest"

    if [[ "$image" =~ : ]]; then
        tag="${image##*:}"
        image="${image%:*}"
    fi

    if [[ "$image" =~ / ]]; then
        registry_name="${image%/*}"
        image_name="${image##*/}"
    else
        image_name="$image"
    fi

    if [[ "$registry_name" == "$image_name" ]]; then
        registry_name="docker.io"
    fi

    # 返回结果
    echo "Result: $registry_name,$image_name,$tag"
}

result=$(parse_image_name "$image_input")
IFS=',' read -r registryName image tag <<< "$result"
echo "Registry Name: $registryName"
echo "Image Name: $image"
echo "Tag: $tag"

if [ "$platform" == "all" ]; then
  sync_image $registryName $image $tag
  sync_image $registryName $image $tag arm64
else
  sync_image $registryName $image $tag $platform
fi
