FROM ubuntu:22.04

# Install necessary packages
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        stress-ng \
        wget \
        gdb \
        ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Add Microsoft package repository
RUN wget -q https://packages.microsoft.com/config/ubuntu/22.04/packages-microsoft-prod.deb -O packages-microsoft-prod.deb && \
    dpkg -i packages-microsoft-prod.deb && \
    rm packages-microsoft-prod.deb

# Update package lists and install procdump
RUN apt-get update && \
    apt-get install -y --no-install-recommends procdump=1.2-2 && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /tmp/dumps
RUN chmod 777 /tmp/dumps
