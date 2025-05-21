FROM golang:1.24.3-bullseye

ARG GIT_VERSION=2.48.1

RUN apt-get update \
  && apt-get install --no-install-recommends -y \
  make \
  sudo \
  vim \
  curl \
  openssh-client \
  gettext \
  libexpat1-dev \
  libz-dev \
  libssl-dev \
  libcurl4-openssl-dev \
  lsb-release \
  locales \
  less \
  mandoc \
  jq \
  xdg-utils \
  vim \
  fonts-ipafont-gothic \
  fonts-ipafont-mincho \
  && cd /usr/local/src \
  && wget https://github.com/git/git/archive/refs/tags/v${GIT_VERSION}.tar.gz \
  && tar -xzf v${GIT_VERSION}.tar.gz \
  && cd git-${GIT_VERSION} \
  && make prefix=/usr/local all -j "$(nproc)" \
  && make prefix=/usr/local install \
  && go install github.com/cweill/gotests/gotests@v1.6.0 \
  && go install github.com/fatih/gomodifytags@v1.17.0 \
  && go install github.com/josharian/impl@v1.4.0 \
  && go install github.com/haya14busa/goplay/cmd/goplay@v1.0.0 \
  && go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.6 \
  && go install golang.org/x/tools/gopls@v0.18.1 \
  && go install github.com/go-delve/delve/cmd/dlv@v1.24.2

ARG USER_NAME=user \
  GROUP_NAME=user \
  USER_ID=1000 \
  GROUP_ID=1000

ENV TERM=xterm \
  DBUS_SESSION_BUS_ADDRESS=/dev/null \
  APP_DIR=/src

RUN groupadd -r -g $GROUP_ID ${GROUP_NAME} \
  && useradd -m -s /bin/bash -u $USER_ID -g $GROUP_ID -G sudo $USER_NAME \
  && echo $USER_NAME:$USER_NAME | chpasswd \
  && echo "$USER_NAME   ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

RUN mkdir -p ${APP_DIR} /home/user/.vscode-server/extensions \
  && chown -R ${USER_NAME}:${GROUP_NAME} /home/${USER_NAME} ${APP_DIR} /home/user/.vscode-server/extensions /go \
  && localedef -f UTF-8 -i ja_JP ja_JP.utf8 \
  && rm -rf /var/lib/apt/lists/*

USER $USER_NAME

ENV TZ=Asia/Tokyo

WORKDIR ${APP_DIR}
