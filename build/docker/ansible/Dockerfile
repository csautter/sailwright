FROM ubuntu:26.04

# Install dependencies
RUN apt-get update && apt-get install -y \
    software-properties-common

RUN add-apt-repository --yes --update ppa:ansible/ansible

RUN apt install -y ansible

RUN ansible --version

RUN apt install -y sudo

# Create a non-root user and add to sudoers
RUN groupadd -g 6500 ansibleuser && \
    useradd -u 6500 -g 6500 -m -s /bin/bash ansibleuser && \
    echo "ansibleuser ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

USER 6500:6500