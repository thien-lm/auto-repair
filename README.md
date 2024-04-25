# Note
this code was stolen 69% from phongvd0403
value inside "()" below can be customize simply
# workflow of controller:
1, controller will monitor every event change to nodes in cluster, if no event was sent, controller have a default resync period (10s) to resync data from KubeAPI server to informer
<br/>
2, when a node is monitored as in "notReady" state for more than (10 minutes), a function to repair that node will be call
<br/>
    2.1 trying to reboot node (n time)
    <br/>
    2.2 trying to replace - scale up and scale down node:
    <ul>
    <li>
        2.2.0 if node can't be remove, or the scale size is conflict with auto scaler made by phongvd0403, do nothing, return error 
    </li>
    <li>
        2.2.1 if number of node is less than max auto scaler size , create then remove node 
    </li>
    <li>
        2.2.2 if number of node is equal or more than max auto scaler size remove node then create new node 
    </li>
    </ul>