package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v4"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/wardviaene/golang-for-devops-course/ssh-demo"
)

const location = "westus"

var (
	virtualNetworksClient   *armnetwork.VirtualNetworksClient
	publicIPAddressesClient *armnetwork.PublicIPAddressesClient
	securityGroupClient     *armnetwork.SecurityGroupsClient
	interfacesClient        *armnetwork.InterfacesClient
	networkClientFactory    *armnetwork.ClientFactory
)

func main() {
	var (
		token  azcore.TokenCredential
		pubKey string
		err    error
	)
	ctx := context.Background()
	subscriptionID := os.Getenv("SUBSCRIPTION_ID")
	if len(subscriptionID) == 0 {
		fmt.Printf("No subscription Id was provided")
		os.Exit(1)
	}
	if pubKey, err = generateKeys(); err != nil {
		fmt.Printf("generatekeys error  %s\n", err)
		os.Exit(1)
	}
	if token, err = getToken(); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}
	if err = launchInstance(ctx, token, subscriptionID, &pubKey); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}
}

func generateKeys() (string, error) {
	var (
		privateKey []byte
		publicKey  []byte
		err        error
	)

	if privateKey, publicKey, err = ssh.GenerateKeys(); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}

	if err = os.WriteFile("mykey.pem", privateKey, 0600); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}

	if err = os.WriteFile("mykey.pub", publicKey, 0644); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}
	return string(publicKey), nil
}

func getToken() (azcore.TokenCredential, error) {
	token, err := azidentity.NewAzureCLICredential(nil)
	if err != nil {
		// handle error
		return token, err
	}
	return token, err
}

func launchInstance(ctx context.Context, cred azcore.TokenCredential, subscriptionID string, pubKey *string) error {
	//Create Resource Client
	clientFactory, err := armresources.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		return err
	}
	resourceGroupParam := armresources.ResourceGroup{
		Location: to.Ptr(location),
	}
	resourceGroupClinet := clientFactory.NewResourceGroupsClient()
	resourceGroupResp, err := resourceGroupClinet.CreateOrUpdate(ctx, "go-azure", resourceGroupParam, nil)
	if err != nil {
		return err
	}
	var vnetResp armnetwork.VirtualNetworksClientCreateOrUpdateResponse
	found, err := findVnet(ctx, *resourceGroupResp.Name, "go-azure", virtualNetworksClient)

	if !found {
		// Create Vnet
		networkClientFactory, err = armnetwork.NewClientFactory(subscriptionID, cred, nil)

		virtualNetworksClient = networkClientFactory.NewVirtualNetworksClient()

		pollerResp, err := virtualNetworksClient.BeginCreateOrUpdate(
			ctx,
			*resourceGroupResp.Name,
			"go-azure",
			armnetwork.VirtualNetwork{
				Location: to.Ptr(location),
				Properties: &armnetwork.VirtualNetworkPropertiesFormat{
					AddressSpace: &armnetwork.AddressSpace{
						AddressPrefixes: []*string{
							to.Ptr("10.1.0.0/16"),
						},
					},
					Subnets: []*armnetwork.Subnet{
						{
							Name: to.Ptr("sample-subnet-0"),
							Properties: &armnetwork.SubnetPropertiesFormat{
								AddressPrefix: to.Ptr("10.1.2.0/16"),
							},
						},
						{
							Name: to.Ptr("sample-subnet-1"),
							Properties: &armnetwork.SubnetPropertiesFormat{
								AddressPrefix: to.Ptr("10.1.3.0/16"),
							},
						},
					},
				},
			},
			nil)

		if err != nil {
			return err
		}

		vnetResp, err = pollerResp.PollUntilDone(ctx, nil)
		if err != nil {
			return err
		}

	}

	IPpollerResp, err := publicIPAddressesClient.BeginCreateOrUpdate(
		ctx,
		*resourceGroupResp.Name,
		"go-azure",
		armnetwork.PublicIPAddress{
			Name:     to.Ptr("go-azure"),
			Location: to.Ptr(location),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAddressVersion:   to.Ptr(armnetwork.IPVersionIPv4),
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			},
		},
		nil,
	)
	if err != nil {
		return err
	}

	ipResp, err := IPpollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	newSGpollerResp, err := securityGroupClient.BeginCreateOrUpdate(
		ctx,
		*resourceGroupResp.Name,
		"go-azure",
		armnetwork.SecurityGroup{
			Location: to.Ptr(location),
			Properties: &armnetwork.SecurityGroupPropertiesFormat{
				SecurityRules: []*armnetwork.SecurityRule{
					{
						Name: to.Ptr("allow_ssh"),
						Properties: &armnetwork.SecurityRulePropertiesFormat{
							Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolTCP),
							SourceAddressPrefix:      to.Ptr("0.0.0.0/0"),
							SourcePortRange:          to.Ptr("1-65535"),
							DestinationAddressPrefix: to.Ptr("0.0.0.0/0"),
							DestinationPortRange:     to.Ptr("22"),
							Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
							Direction:                to.Ptr(armnetwork.SecurityRuleDirectionInbound),
							Priority:                 to.Ptr[int32](100),
						},
					},
					{
						Name: to.Ptr("allow_https"),
						Properties: &armnetwork.SecurityRulePropertiesFormat{
							Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolTCP),
							SourceAddressPrefix:      to.Ptr("0.0.0.0/0"),
							SourcePortRange:          to.Ptr("1-65535"),
							DestinationAddressPrefix: to.Ptr("0.0.0.0/0"),
							DestinationPortRange:     to.Ptr("443"),
							Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
							Direction:                to.Ptr(armnetwork.SecurityRuleDirectionInbound),
							Priority:                 to.Ptr[int32](200),
						},
					},
				},
			},
		},
		nil)

	if err != nil {
		return err
	}

	sgResp, err := newSGpollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	newNICpollerResp, err := interfacesClient.BeginCreateOrUpdate(
		ctx,
		*resourceGroupResp.Name,
		"go-azure",
		armnetwork.Interface{
			Location: to.Ptr(location),
			Properties: &armnetwork.InterfacePropertiesFormat{
				IPConfigurations: []*armnetwork.InterfaceIPConfiguration{
					{
						Name: to.Ptr("ipConfig"),
						Properties: &armnetwork.InterfaceIPConfigurationPropertiesFormat{
							PrivateIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodDynamic),
							Subnet: &armnetwork.Subnet{
								ID: to.Ptr(*vnetResp.Properties.Subnets[0].ID),
							},
							PublicIPAddress: &armnetwork.PublicIPAddress{
								ID: to.Ptr(*ipResp.ID),
							},
						},
					},
				},
				NetworkSecurityGroup: &armnetwork.SecurityGroup{
					ID: to.Ptr(*sgResp.ID),
				},
			},
		},
		nil,
	)
	if err != nil {
		return nil
	}

	ntResp, err := newNICpollerResp.PollUntilDone(ctx, nil)
	if err != nil {
		return nil
	}

	// Launch VM

	fmt.Println("Creating Virtual Machine")
	computeClientFactory, err := armcompute.NewClientFactory(subscriptionID, cred, nil)
	if err != nil {
		log.Fatal(err)
	}
	virtualMachinesClient := computeClientFactory.NewVirtualMachinesClient()
	// disksClient := computeClientFactory.NewDisksClient()

	parameters := armcompute.VirtualMachine{
		Location: to.Ptr(location),
		Identity: &armcompute.VirtualMachineIdentity{
			Type: to.Ptr(armcompute.ResourceIdentityTypeNone),
		},
		Properties: &armcompute.VirtualMachineProperties{
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: &armcompute.ImageReference{
					// search image reference
					// az vm image list --output table
					// Offer:     to.Ptr("WindowsServer"),
					// Publisher: to.Ptr("MicrosoftWindowsServer"),
					// SKU:       to.Ptr("2019-Datacenter"),
					// Version:   to.Ptr("latest"),
					//require ssh key for authentication on linux
					Offer:     to.Ptr("UbuntuServer"),
					Publisher: to.Ptr("Canonical"),
					SKU:       to.Ptr("18.04-LTS"),
					Version:   to.Ptr("latest"),
				},
				OSDisk: &armcompute.OSDisk{
					Name:         to.Ptr("go-azure"),
					CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					Caching:      to.Ptr(armcompute.CachingTypesReadWrite),
					ManagedDisk: &armcompute.ManagedDiskParameters{
						StorageAccountType: to.Ptr(armcompute.StorageAccountTypesStandardLRS), // OSDisk type Standard/Premium HDD/SSD
					},
					//DiskSizeGB: to.Ptr[int32](100), // default 127G
				},
			},
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: to.Ptr(armcompute.VirtualMachineSizeTypes("Standard_F2s")), // VM size include vCPUs,RAM,Data Disks,Temp storage.
			},
			OSProfile: &armcompute.OSProfile{ //
				ComputerName:  to.Ptr("go-azure"),
				AdminUsername: to.Ptr("sample-user"),
				// AdminPassword: to.Ptr("Password01!@#"),
				//require ssh key for authentication on linux
				LinuxConfiguration: &armcompute.LinuxConfiguration{
					DisablePasswordAuthentication: to.Ptr(true),
					SSH: &armcompute.SSHConfiguration{
						PublicKeys: []*armcompute.SSHPublicKey{
							{
								Path:    to.Ptr(fmt.Sprintf("/home/%s/.ssh/authorized_keys", "sample-user")),
								KeyData: pubKey,
							},
						},
					},
				},
			},
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: to.Ptr(*ntResp.ID),
					},
				},
			},
		},
	}

	VMpollerResponse, err := virtualMachinesClient.BeginCreateOrUpdate(ctx, *resourceGroupResp.Name, "go-azure", parameters, nil)
	if err != nil {
		return err
	}

	newVmResp, err := VMpollerResponse.PollUntilDone(ctx, nil)
	if err != nil {
		return err
	}

	fmt.Printf("Virtual Machine %s\n", *newVmResp.ID)

	return nil
	// return nil
}

func findVnet(ctx context.Context, resourceGroupName string, vnetName string, vnetClient *armnetwork.VirtualNetworksClient) (bool, error) {
	_, err := vnetClient.Get(ctx, resourceGroupName, vnetName, nil)
	if err != nil {
		var errResponse *azcore.ResponseError
		if errors.As(err, &errResponse) && errResponse.ErrorCode == "ResourceNotFound" {
			return false, nil
		}
		return false, err
	}
	return true, nil

}
