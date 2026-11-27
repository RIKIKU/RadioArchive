# trunk-recorder with RadioArchive's recorder plugin (SPEC.md R1, R86;
# ADR 0001). amd64 and arm64. Build context: recorder/plugin.
#
#   docker buildx build recorder/plugin -t radioarchive-recorder:dev
#   docker buildx build recorder/plugin --target so --output type=local,dest=out
#
# The second gives out/libradioarchive.so for a native install.
#
# The pin is trunk-recorder v5.2.1, twice: the official image by digest, and
# the release's source by tag and commit. The build fails unless the
# headers installed in the image are byte for byte the tag's, so the plugin
# can't be built against anything but the release it says. Upgrading
# trunk-recorder means changing all three and passing CI.
ARG TR_IMAGE=robotastic/trunk-recorder:5.2.1@sha256:c3e425858bfca16c609d8612808a83f06b0f376d950d11733d7c25b2a28f8866
ARG UBUNTU_IMAGE=ubuntu:24.04@sha256:562111b88caa79c71391c0c4d3c2114a7d5c35915c20561fd893cb1e07929e6b

FROM ${TR_IMAGE} AS trunk-recorder

# trunk-recorder's image is built on ubuntu:24.04 with these packages, so the
# plugin compiles with the same compiler, Boost and GNU Radio.
FROM ${UBUNTU_IMAGE} AS toolchain
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install --no-install-recommends -y \
      build-essential ca-certificates cmake git gnuradio-dev libboost-dev \
      libboost-filesystem-dev libboost-log-dev libboost-program-options-dev \
 && rm -rf /var/lib/apt/lists/*

FROM toolchain AS source
ARG TR_TAG=v5.2.1
ARG TR_COMMIT=f0ace2cb6e4c6c69009aa9b8c9c3bc5c2824cffa
RUN git clone --quiet --depth 1 --branch "${TR_TAG}" https://github.com/TrunkRecorder/trunk-recorder.git /tr \
 && test "$(git -C /tr rev-parse HEAD)" = "${TR_COMMIT}" \
 || { echo "trunk-recorder ${TR_TAG} isn't commit ${TR_COMMIT}" >&2; exit 1; }
COPY --from=trunk-recorder /usr/local/include/trunk-recorder /image-include
RUN cd /tr/trunk-recorder \
 && find . -name '*.h' | sort > /tmp/tag-headers \
 && (cd /image-include && find . -name '*.h' | sort) > /tmp/image-headers \
 && diff /tmp/tag-headers /tmp/image-headers \
 && while read -r h; do cmp "$h" "/image-include/$h"; done < /tmp/tag-headers \
 && cmp /tr/lib/json.hpp /image-include/json.hpp \
 && echo "ok: the image's headers are ${TR_TAG}'s"

FROM toolchain AS build
COPY --from=source /image-include /usr/local/include/trunk-recorder
COPY CMakeLists.txt wire.h conclusion.h conclusion_test.cc radioarchive.cc /src/
# The plugin's own unit test (ADR 0053) runs first, so a failure fails the build.
RUN g++ -std=c++17 -Wall -Wextra -o /tmp/conclusion_test /src/conclusion_test.cc && /tmp/conclusion_test \
 && cmake -S /src -B /build \
 && cmake --build /build -j"$(nproc)" \
 && strip --strip-unneeded /build/libradioarchive.so

# p25gen, the synthetic P25 I/Q fixture generator for the CI test, built
# from the pinned release's own P25 encoders and IMBE vocoder. Test only:
#   docker buildx build recorder/plugin --target p25gen --output type=local,dest=out
FROM source AS p25gen-build
COPY fixture/p25gen.cc /src/fixture/
RUN cd /tr/lib/op25_repeater/lib \
 && g++ -std=c++17 -O2 -w -I. -Iimbe_vocoder -o /p25gen /src/fixture/p25gen.cc imbe_vocoder/*.cc

FROM scratch AS p25gen
COPY --from=p25gen-build /p25gen /

# The .so alone, for native installs.
FROM scratch AS so
COPY --from=build /build/libradioarchive.so /

FROM trunk-recorder
COPY --from=build /build/libradioarchive.so /usr/local/lib/trunk-recorder/libradioarchive.so
# The plugin's .so loads against this image's own libraries.
RUN ldd /usr/local/lib/trunk-recorder/libradioarchive.so | tee /dev/stderr | (! grep -q 'not found')
LABEL org.opencontainers.image.source="https://github.com/RIKIKU/RadioArchive" \
      org.opencontainers.image.description="trunk-recorder v5.2.1 with RadioArchive's recorder plugin" \
      org.opencontainers.image.licenses="GPL-3.0-or-later AND MIT"
