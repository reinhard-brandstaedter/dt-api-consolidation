#!/usr/bin/env ruby

require 'aws-sdk'

#
# This script requires you to have the following environment variables set:
#   AWS_REGION="us-west-2"
#   AWS_ACCESS_KEY_ID="<YOUR_KEY>"
#   AWS_SECRET_ACCESS_KEY="<YOUR_SECRET_KEY>"
#
# Based on https://gist.github.com/asimihsan/d8d8f0f10bdc85fc6f8a
#

def find_hosted_zone(route53, domain)
  route53 = Aws::Route53::Client.new
  hosted_zones = route53.list_hosted_zones_by_name.hosted_zones

  # original: will not work with subdomains hosted by route53
  # index = hosted_zones.index { |zone| domain.end_with?(zone.name.chop) }
  # instead of matching the domain with the zone, do reverse
  index = hosted_zones.index { |zone| zone.name.chop.end_with?(domain.split(".",2)[1]) }
  if index.nil?
    puts 'Unable to find matching zone.'
    exit 1
  end

  hosted_zones[index]
end

def wait_for_change(route53, change_id)
  status = ''
  until status == 'INSYNC'
    resp = route53.get_change(id: change_id)
    status = resp.change_info.status
    if status != 'INSYNC'
      puts 'Waiting for dns change to complete'
      sleep 5
    end
  end
end

def setup_dns(domain, txt_challenge)
  route53 = Aws::Route53::Client.new
  hosted_zone = find_hosted_zone(route53, domain)

  changes = []
  changes << {
    action: 'UPSERT',
    resource_record_set: {
      name: "_acme-challenge.#{domain}.",
      type: 'TXT',
      ttl: 60,
      resource_records: [
        value: "\"#{txt_challenge}\""
      ]
    }
  }
  resp = route53.change_resource_record_sets(
    hosted_zone_id: hosted_zone.id,
    change_batch: {
      changes: changes
    }
  )
  wait_for_change(route53, resp.change_info.id)
end

def delete_dns(domain, txt_challenge)
  route53 = Aws::Route53::Client.new
  hosted_zone = find_hosted_zone(route53, domain)
  changes = []
  changes << {
    action: 'DELETE',
    resource_record_set: {
      name: "_acme-challenge.#{domain}.",
      type: 'TXT',
      ttl: 60,
      resource_records: [
        value: "\"#{txt_challenge}\""
      ]
    }
  }
  resp = route53.change_resource_record_sets(
    hosted_zone_id: hosted_zone.id,
    change_batch: {
      changes: changes
    }
  )
  wait_for_change(route53, resp.change_info.id)
end

if __FILE__ == $PROGRAM_NAME
  hook_stage = ARGV[0]
  domain = ARGV[1]
  txt_challenge = ARGV[3]

  puts "stage: #{hook_stage} domain: #{domain} txt_challenge: #{txt_challenge}"

  if hook_stage == 'deploy_challenge'
    setup_dns(domain, txt_challenge)
  elsif hook_stage == 'clean_challenge'
    delete_dns(domain, txt_challenge)
  end
end
