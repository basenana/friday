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

  echo "Syncing image: $image, registry name: $registryName, platform: ${platform:-amd64}"

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

sync_multi_platform_image() {
    local registryName=$1
    local image=$2
    local tag=${3:-latest}
    oldImage="${registryName}/${image}:${tag}"
    archs=("amd64" "arm64")

    if [ -z "$oldImage" ]; then
        echo "old image is empty"
        return 1
    fi

    for REGION in ${REGIONS[@]};
    do
      echo "in ${REGION}"
      newImage="${REGION}/hdls/${image}:${tag}"
      for arch in "${archs[@]}"; do
          arch_tag=$(echo "$arch" | sed 's/\//_/g')
          tagged_image="${newImage}-${arch_tag}"

          echo "Processing $arch: $oldImage => $tagged_image"

          docker pull --platform $arch $oldImage
          docker tag $oldImage $tagged_image
          docker push $tagged_image
      done

      docker manifest create ${newImage} ${newImage}-arm64 ${newImage}-amd64
      docker manifest push ${newImage}
      sleep 5
    done
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

    echo "$registry_name,$image_name,$tag"
}

result=$(parse_image_name "$image_input")
IFS=',' read -r registryName image tag <<< "$result"
echo "Registry Name: $registryName"
echo "Image Name: $image"
echo "Tag: $tag"

if [ "$platform" == "all" ]; then
  sync_multi_platform_image $registryName $image $tag
else
  sync_image $registryName $image $tag $platform
fi
