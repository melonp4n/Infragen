M ubuntu:latest

# Prevent interactive prompts during installation
ENV DEBIAN_FRONTEND=noninteractive

# Install SSH and create the privilege separation directory
RUN apt-get update && apt-get install -y openssh-server

# Set root password and permit root login
RUN echo 'root:{password}' | chpasswd \
    && sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config

RUN echo '{ssh_pubkey}' > '/root/.ssh/authorized_keys'
RUN chmod 700 /root/.ssh
RUN chmod 600 /root/.ssh/authorized_keys

EXPOSE 22

ENTRYPOINT ["/usr/sbin/sshd", "-D"]

