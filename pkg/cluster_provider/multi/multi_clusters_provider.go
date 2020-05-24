package multi

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/turbinelabs/api"
	"github.com/turbinelabs/nonstdlib/log/console"
	"github.com/turbinelabs/rotor/pkg/cluster_provider"
	"github.com/turbinelabs/rotor/plugins/aws"
	"github.com/turbinelabs/rotor/plugins/multi"
	"io/ioutil"
	"os"
)

type multiClustersProvider struct {
	clusterProviders []cluster_provider.ClusterProvider
}


type ClustersProviderConfig struct {
	ConfigFileLocation string
}


func (m *multiClustersProvider) UnmarshalJSON(data []byte) error {
	type temp struct {
		ClustersProviders []struct{
			Type   string      `json:"type"`
			Config *json.RawMessage `json:"config"`
		}  `json:"clusters_providers"`
	}

	t := &temp{}

	err := json.Unmarshal(data, t)
	if err != nil {
		return err
	}

	m.clusterProviders = []cluster_provider.ClusterProvider{}

	for _, v := range t.ClustersProviders {
		switch v.Type {
		case string(multi.ECSClustersProviderConfigType):
			c := aws.ECSClustersProviderConfig{
				Clusters:   []string{},
				Aws:        aws.ECSAWSConfig{},
			}
			err = json.Unmarshal(*v.Config, &c)
			if err != nil {
				return err
			}
			cp, err := aws.NewECSClusterProvider(c)
			if err != nil {
				return err
			}
			m.clusterProviders = append(m.clusterProviders, cp)
		case string(multi.EC2ClustersProviderConfigType):
			c := aws.EC2ClustersProviderConfig{
				Filters:   map[string][]string {},
				Aws:       aws.EC2AWSConfig{},
			}
			err = json.Unmarshal(*v.Config, &c)
			if err != nil {
				return err
			}
			cp, err := aws.NewEC2ClusterProvider(c)
			if err != nil {
				return err
			}
			m.clusterProviders = append(m.clusterProviders, cp)
		default:
			return errors.New(fmt.Sprintf(
				"ClustersProviderConfig: unknown cluster provider type: %s, expected: one of %v",
				v.Type,
				[]multi.ConfigType{multi.ECSClustersProviderConfigType, multi.EC2ClustersProviderConfigType},
			))
		}
	}

	return nil
}

func NewMultiClustersProvider(config ClustersProviderConfig) (cluster_provider.ClusterProvider, error) {
	fp, err := os.Open(config.ConfigFileLocation)
	if err != nil {
		return nil, err
	}
	bts, err := ioutil.ReadAll(fp)
	if err != nil {
		return nil, err
	}
	p := &multiClustersProvider{}
	err = json.Unmarshal(bts, p)
	if err != nil {
		return nil, err
	}
	return p, nil
}

type pairClustersError struct {
	provider cluster_provider.ClusterProvider
	clusters []api.Cluster
	err      error
}

func getClustersFromProvider(provider cluster_provider.ClusterProvider, ch chan pairClustersError) {
	defer func() {
		if r := recover(); r != nil {
			err := errors.New(fmt.Sprintf("%v", r))
			ch <- pairClustersError{
				clusters: nil,
				err:      err,
			}
		}
	}()

	cs, err := provider.GetClusters()
	ch <- pairClustersError{
		provider: provider,
		clusters: cs,
		err:      err,
	}
}

func (m *multiClustersProvider) GetClusters() ([]api.Cluster, error) {
	var clusters []api.Cluster

	ch := make(chan pairClustersError)

	for _, cp := range m.clusterProviders {
		go getClustersFromProvider(cp, ch)
	}

	set := map[string]cluster_provider.ClusterProvider{}
	var errs []error
	for i := 0; i < len(m.clusterProviders); i++ {
		p := <- ch
		cs, err := p.clusters, p.err
		if err != nil {
			console.Error().Printf(
				"unable to fetch clusters from cluster provider(%s), error: %v",
				p.provider.String(), err,
			)
			errs = append(errs, err)
		}
		for _, c := range cs {
			if _, ok := set[c.Name]; ok {
				console.Error().Printf(
					"duplicate cluster from clusters provider, %s exists in both cluster providers \n" +
						"cluster_provider 1: %s\n" +
						"cluster_provider 2: %s\n",
					c.Name,
					p.provider.String(),
					set[c.Name].String(),
				)
				continue
			}
			set[c.Name] = p.provider
			clusters = append(clusters, c)
		}
 	}
 	if len(errs) == len(m.clusterProviders) {
 		return nil, errors.New(fmt.Sprintf("%v", errs))
	}
 	return clusters, nil
}
