FROM python:3.7.0-alpine

RUN pip install requests
RUN pip install redis

COPY tenanthelpers /tenanthelpers
COPY tenantmanager.py /tenantmanager.py

CMD ["python", "/tenantmanager.py"]

