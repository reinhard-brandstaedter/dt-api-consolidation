FROM nginx:stable

RUN mkdir -p /app/dehydrated
RUN mkdir -p /etc/dehydrated
RUN rm -f /etc/dehydrated/lock


RUN apt-get update -y
RUN apt-get install -y ca-certificates cron curl git python-setuptools ruby
RUN echo "install: --no-rdoc --no-ri" > ~/.gemrc
RUN echo "update: --no-rdoc --no-ri" >>  ~/.gemrc
RUN gem install aws-sdk

WORKDIR /app/dehydrated
RUN rm -rf /app/dehydrated/*
RUN git clone -n https://github.com/lukas2511/dehydrated.git ./
RUN git checkout 05eda91

COPY config /etc/dehydrated
COPY domains.txt /etc/dehydrated
COPY route53.rb /app/dehydrated
RUN chmod 755 /app/dehydrated/route53.rb

RUN apt-get update -y
RUN apt-get install -y python-pip
RUN pip install requests[security]

COPY ./crontab /etc/crontab
RUN crontab /etc/crontab
RUN touch /var/log/cron

COPY nginx.conf /etc/nginx/nginx.conf
RUN sed -i s/DOMAINNAME/$(cat /etc/dehydrated/domains.txt)/g /etc/nginx/nginx.conf
#RUN mkdir -p /etc/dehydrated/certs/$(cat /etc/dehydrated/domains.txt)
# Generate a .htpassword file for basic auth
# RUN printf "${API_USER}:$(openssl passwd -apr1 ${API_PWD})\n" >> /etc/nginx/.htpasswd
#COPY .htpasswd /etc/nginx/.htpasswd

COPY ./startup.sh /startup.sh
RUN chmod 755 /startup.sh
CMD [ "/startup.sh" ]

#CMD ["nginx", "-g", "daemon off;"]