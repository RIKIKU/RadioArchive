# The recorder plugin, built out of tree against an installed trunk-recorder:
# its headers (TR_INCLUDE_DIR, which trunk-recorder's own install puts in
# /usr/local/include/trunk-recorder) plus the Boost and GNU Radio headers they
# include. Every type the plugin touches is an interface or a struct from
# those headers, so it links nothing of trunk-recorder's.
#
#   cmake -S recorder/plugin -B build && cmake --build build
#
# gives build/libradioarchive.so. recorder/plugin/Dockerfile builds it against
# the pinned release.
cmake_minimum_required(VERSION 3.16)
project(radioarchive_plugin LANGUAGES CXX VERSION 0.2.0)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE Release)
endif()

set(TR_INCLUDE_DIR /usr/local/include/trunk-recorder CACHE PATH "trunk-recorder's installed headers")
if(NOT EXISTS ${TR_INCLUDE_DIR}/plugin_manager/plugin_api.h)
  message(FATAL_ERROR "trunk-recorder headers not found in ${TR_INCLUDE_DIR}")
endif()

find_package(Boost REQUIRED COMPONENTS log)
find_package(Gnuradio REQUIRED COMPONENTS runtime blocks fft filter analog)
# trunk-recorder's headers pick GNU Radio includes by this macro, computed as
# trunk-recorder's own CMakeLists.txt does (0x030a09 for 3.10.9).
math(EXPR GNURADIO_VERSION "(${Gnuradio_VERSION_MAJOR} / 10) << 20 | (${Gnuradio_VERSION_MAJOR} % 10) << 16 | (${Gnuradio_VERSION_MINOR} / 10) << 12 | (${Gnuradio_VERSION_MINOR} % 10) << 8 | (${Gnuradio_VERSION_PATCH} / 10) << 4 | (${Gnuradio_VERSION_PATCH} % 10)")

add_library(radioarchive MODULE radioarchive.cc)
target_include_directories(radioarchive PRIVATE ${TR_INCLUDE_DIR})
target_compile_definitions(radioarchive PRIVATE
  BOOST_LOG_DYN_LINK
  GNURADIO_VERSION=${GNURADIO_VERSION}
  RADIOARCHIVE_PLUGIN_VERSION="${PROJECT_VERSION}")
target_compile_options(radioarchive PRIVATE -Wall -Wextra -Wno-unused-parameter)
# Every symbol the plugin needs must resolve at build time, so a load failure
# in trunk-recorder ("undefined symbol") fails the build instead.
target_link_options(radioarchive PRIVATE -Wl,--no-undefined)
# The same shared Boost and GNU Radio libraries trunk-recorder is linked with
# (the linker keeps only those used).
target_link_libraries(radioarchive PRIVATE
  Boost::log
  gnuradio::gnuradio-runtime
  gnuradio::gnuradio-blocks
  gnuradio::gnuradio-filter
  gnuradio::gnuradio-analog)
install(TARGETS radioarchive LIBRARY DESTINATION lib/trunk-recorder)
